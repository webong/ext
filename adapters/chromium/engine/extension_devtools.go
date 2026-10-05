package chromium

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/webong/ctx/res/browser/extension"
)

// DevToolsTarget is a browser and user data directory selected by the caller.
// The executable must support Chromium's pipe-only Extensions.loadUnpacked.
type DevToolsTarget struct {
	Browser               string
	ExecutablePath        string
	ProfilePath           string
	ProfileDirectory      string
	Headless              bool
	ExtensionPage         string
	RestrictedProfilePath string
}

// RunWithDevTools loads a reviewed extension in a caller-selected browser
// session without the Load unpacked UI. It remains active only while this
// browser session is running; this is not a persistent native installation.
func RunWithDevTools(ctx context.Context, target DevToolsTarget, source, destination, expectedRevision string, ready func(extension.InstallResult) error) error {
	if target.ExtensionPage == "" {
		return errors.New("this target has no pipe-based extension session route on this platform")
	}
	if expectedRevision == "" {
		return errors.New("extension session requires a prepared revision")
	}
	if ready == nil {
		return errors.New("extension session requires a ready callback")
	}
	if !filepath.IsAbs(target.ExecutablePath) || !filepath.IsAbs(target.ProfilePath) {
		return errors.New("browser executable and profile paths must be absolute")
	}
	if target.RestrictedProfilePath != "" && sameExtensionPath(target.ProfilePath, target.RestrictedProfilePath) {
		return errors.New("The selected browser does not allow debugging-pipe session loading in its default user data directory; select a custom user data directory")
	}
	if target.ProfileDirectory != "" && (target.ProfileDirectory == "." || target.ProfileDirectory == ".." || filepath.Base(target.ProfileDirectory) != target.ProfileDirectory) {
		return errors.New("profile directory must be a single directory name")
	}
	info, err := os.Stat(target.ExecutablePath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) {
		return errors.New("browser executable path is not executable")
	}
	profile, err := filepath.Abs(target.ProfilePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(profile, 0700); err != nil {
		return err
	}
	description, err := extension.StageWithRevision(source, destination, expectedRevision)
	if err != nil {
		return err
	}
	staged, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	selectedProfile := profile
	if target.ProfileDirectory != "" {
		selectedProfile = filepath.Join(profile, target.ProfileDirectory)
	}
	return runUnpackedViaPipe(ctx, target, profile, staged, func(id string) error {
		return ready(extension.InstallResult{Status: "activated", Browser: target.Browser, ID: id, Source: staged, Profile: selectedProfile, Extension: &description,
			NextAction: "Keep this command running to keep the browser session and extension active."})
	})
}

type cdpResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func runUnpackedViaPipe(ctx context.Context, target DevToolsTarget, profile, staged string, ready func(string) error) error {
	args := []string{"--remote-debugging-pipe", "--enable-unsafe-extension-debugging", "--user-data-dir=" + profile, "--no-first-run", "--no-default-browser-check"}
	if target.ProfileDirectory != "" {
		args = append(args, "--profile-directory="+target.ProfileDirectory)
	}
	if target.Headless {
		args = append(args, "--headless=new")
	}
	session, err := launchDebuggingPipe(target.ExecutablePath, args)
	if err != nil {
		return err
	}
	defer session.kill()
	installContext, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := writeCDPRequest(session.request, 1, "Extensions.loadUnpacked", map[string]any{"path": staged}); err != nil {
		return fmt.Errorf("send extension load request: %w", err)
	}
	response, err := awaitCDPResponse(installContext, 1, session.responses, session.failures, session.exited, session.stderr)
	if err != nil {
		return err
	}
	var installed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Result, &installed); err != nil || installed.ID == "" {
		return errors.New("browser did not return a loaded extension ID")
	}
	if err := writeCDPRequest(session.request, 2, "Extensions.getExtensions", nil); err != nil {
		return err
	}
	verified, err := awaitCDPResponse(installContext, 2, session.responses, session.failures, session.exited, session.stderr)
	if err != nil {
		return err
	}
	var listed struct {
		Extensions []struct {
			ID string `json:"id"`
		} `json:"extensions"`
	}
	if err := json.Unmarshal(verified.Result, &listed); err != nil {
		return fmt.Errorf("invalid browser extension inventory: %w", err)
	}
	found := false
	for _, item := range listed.Extensions {
		if item.ID == installed.ID {
			found = true
			break
		}
	}
	if !found {
		return errors.New("browser did not report the newly loaded extension")
	}
	if err := ready(installed.ID); err != nil {
		return err
	}
	select {
	case <-session.exited:
	case <-ctx.Done():
		_ = writeCDPRequest(session.request, 3, "Browser.close", nil)
		select {
		case <-session.exited:
		case <-time.After(10 * time.Second):
			return errors.New("browser did not close after session cancellation")
		}
	}
	return nil
}

func writeCDPRequest(writer io.Writer, id int, method string, params map[string]any) error {
	value := map[string]any{"id": id, "method": method}
	if params != nil {
		value["params"] = params
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encoded = append(encoded, 0)
	_, err = writer.Write(encoded)
	return err
}

func readCDPResponses(reader io.Reader, output chan<- cdpResponse, failures chan<- error) {
	buffer := bufio.NewReaderSize(reader, 2<<20)
	for {
		message, err := buffer.ReadSlice(0)
		if err != nil {
			failures <- err
			return
		}
		var response cdpResponse
		if json.Unmarshal(bytes.TrimSuffix(message, []byte{0}), &response) == nil && response.ID != 0 {
			output <- response
		}
	}
}

func awaitCDPResponse(ctx context.Context, id int, responses <-chan cdpResponse, failures <-chan error, exited <-chan error, stderr *limitedWriter) (cdpResponse, error) {
	for {
		select {
		case response := <-responses:
			if response.ID != id {
				continue
			}
			if response.Error != nil {
				return cdpResponse{}, fmt.Errorf("browser rejected extension request: %s", response.Error.Message)
			}
			return response, nil
		case err := <-failures:
			return cdpResponse{}, fmt.Errorf("browser debugging pipe closed: %w (%s)", err, strings.TrimSpace(stderr.String()))
		case err := <-exited:
			return cdpResponse{}, fmt.Errorf("browser exited during extension installation: %v (%s)", err, strings.TrimSpace(stderr.String()))
		case <-ctx.Done():
			return cdpResponse{}, ctx.Err()
		}
	}
}

type limitedWriter struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	remaining int
}

func (value *limitedWriter) Write(data []byte) (int, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	length := len(data)
	if value.remaining > 0 {
		kept := data
		if len(kept) > value.remaining {
			kept = kept[:value.remaining]
		}
		_, _ = value.buffer.Write(kept)
		value.remaining -= len(kept)
	}
	return length, nil
}

func (value *limitedWriter) String() string {
	value.mu.Lock()
	defer value.mu.Unlock()
	return value.buffer.String()
}
