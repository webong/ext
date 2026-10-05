package chromium

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/webong/ctx/res/browser/extension"
)

// InstallWithNativeUI guides a persistent Load unpacked installation in the
// selected Chromium-family profile. The browser's own UI performs the install;
// the adapter observes it and verifies that it survives a browser restart.
// The selected profile must be closed before this command starts.
func InstallWithNativeUI(ctx context.Context, target DevToolsTarget, source, destination, expectedRevision string, report func(extension.InstallResult) error) error {
	if target.ExtensionPage == "" {
		return errors.New("this target has no native extension installation route on this platform")
	}
	if target.Headless {
		return errors.New("native extension installation requires a visible browser")
	}
	if expectedRevision == "" {
		return errors.New("extension installation requires a prepared revision")
	}
	if report == nil {
		return errors.New("extension installation requires a status callback")
	}
	if !filepath.IsAbs(target.ExecutablePath) || !filepath.IsAbs(target.ProfilePath) || !filepath.IsAbs(destination) {
		return errors.New("browser executable, profile, and destination paths must be absolute")
	}
	if target.ProfileDirectory == "" {
		target.ProfileDirectory = "Default"
	}
	if target.ProfileDirectory == "." || target.ProfileDirectory == ".." || filepath.Base(target.ProfileDirectory) != target.ProfileDirectory {
		return errors.New("profile directory must be a single directory name")
	}
	if target.RestrictedProfilePath != "" && sameExtensionPath(target.ProfilePath, target.RestrictedProfilePath) {
		description, err := stageForNativeInstall(source, destination, expectedRevision)
		if err != nil {
			return err
		}
		return report(extension.InstallResult{Status: "awaiting-browser-action", Browser: target.Browser, Source: destination, Profile: filepath.Join(target.ProfilePath, target.ProfileDirectory), Extension: &description, NextAction: "Open the selected profile and its extensions page, enable Developer mode, and Load unpacked from the staged directory. Keep that directory in place. This profile requires manual installation; persistence has not been verified."})
	}
	info, err := os.Stat(target.ExecutablePath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) {
		return errors.New("browser executable path is not executable")
	}
	if err := os.MkdirAll(target.ProfilePath, 0700); err != nil {
		return err
	}
	description, err := stageForNativeInstall(source, destination, expectedRevision)
	if err != nil {
		return err
	}
	page := target.ExtensionPage
	first, err := startNativeBrowser(target, page)
	if err != nil {
		return err
	}
	defer first.kill()
	probeContext, stopProbe := context.WithTimeout(ctx, 30*time.Second)
	_, err = first.extensions(probeContext)
	stopProbe()
	if err != nil {
		return fmt.Errorf("selected browser did not open its extension inventory: %w", err)
	}
	if err := report(extension.InstallResult{
		Status: "awaiting-browser-action", Browser: target.Browser, Source: destination,
		Profile: filepath.Join(target.ProfilePath, target.ProfileDirectory), Extension: &description,
		NextAction: "In the opened browser, enable Developer mode, select Load unpacked, and choose the staged directory. Keep the installation session running for restart verification, and keep the staged directory in place.",
	}); err != nil {
		return err
	}
	item, err := first.waitForPath(ctx, destination)
	if err != nil {
		return err
	}
	if err := first.close(); err != nil {
		return fmt.Errorf("close browser for installation verification: %w", err)
	}
	second, err := startNativeBrowser(target, page)
	if err != nil {
		return fmt.Errorf("restart browser for installation verification: %w", err)
	}
	defer second.kill()
	verified, err := second.waitForPathWithTimeout(ctx, destination, 30*time.Second)
	if err != nil {
		return fmt.Errorf("extension did not survive browser restart: %w", err)
	}
	if verified.ID != item.ID || !verified.Enabled {
		return errors.New("extension did not remain enabled with the same ID after browser restart")
	}
	current, err := extension.Inspect(destination)
	if err != nil || current.Revision != expectedRevision {
		return errors.New("staged extension changed during installation")
	}
	if err := report(extension.InstallResult{
		Status: "installed", Browser: target.Browser, ID: verified.ID,
		Source: destination, Profile: filepath.Join(target.ProfilePath, target.ProfileDirectory), Extension: &current,
		NextAction: "Keep the staged directory in place; the browser loads this unpacked extension from that path.",
	}); err != nil {
		return err
	}
	// Leave the verified browser open for the user. Closing it or cancelling the
	// command ends the process, but the native installation remains in profile.
	select {
	case <-second.exited:
		return nil
	case <-ctx.Done():
		return second.close()
	}
}

func stageForNativeInstall(source, destination, expectedRevision string) (extension.Description, error) {
	sourcePath, err := filepath.Abs(source)
	if err != nil {
		return extension.Description{}, err
	}
	destinationPath, err := filepath.Abs(destination)
	if err != nil {
		return extension.Description{}, err
	}
	if destinationPath == sourcePath || strings.HasPrefix(destinationPath, sourcePath+string(filepath.Separator)) {
		return extension.Description{}, errors.New("destination must be outside the source")
	}
	prepared, err := extension.Inspect(source)
	if err != nil {
		return extension.Description{}, err
	}
	if prepared.Revision != expectedRevision {
		return extension.Description{}, errors.New("extension revision changed since inspection")
	}
	if _, err := os.Stat(destination); err == nil {
		staged, err := extension.Inspect(destination)
		if err != nil || staged.Revision != expectedRevision {
			return extension.Description{}, errors.New("existing staged directory differs from the prepared revision")
		}
		return staged, nil
	} else if !os.IsNotExist(err) {
		return extension.Description{}, err
	}
	return extension.StageWithRevision(source, destination, expectedRevision)
}

type nativeExtension struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
}

type nativeBrowser struct {
	command   *exec.Cmd
	request   *os.File
	response  *os.File
	responses chan cdpResponse
	failures  chan error
	exited    chan error
	stderr    *limitedWriter
	nextID    int
}

func startNativeBrowser(target DevToolsTarget, page string) (*nativeBrowser, error) {
	args := []string{
		"--remote-debugging-pipe", "--enable-unsafe-extension-debugging",
		"--user-data-dir=" + target.ProfilePath, "--profile-directory=" + target.ProfileDirectory,
		"--no-first-run", "--no-default-browser-check", page,
	}
	return launchDebuggingPipe(target.ExecutablePath, args)
}

func launchDebuggingPipe(executable string, args []string) (*nativeBrowser, error) {
	requestRead, requestWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	responseRead, responseWrite, err := os.Pipe()
	if err != nil {
		requestRead.Close()
		requestWrite.Close()
		return nil, err
	}
	command := exec.Command(executable, args...)
	stderr := &limitedWriter{remaining: 16 << 10}
	command.Stderr = stderr
	if err := startDebuggingPipe(command, requestRead, responseWrite); err != nil {
		requestRead.Close()
		requestWrite.Close()
		responseRead.Close()
		responseWrite.Close()
		return nil, err
	}
	requestRead.Close()
	responseWrite.Close()
	session := &nativeBrowser{
		command: command, request: requestWrite, response: responseRead,
		responses: make(chan cdpResponse, 4), failures: make(chan error, 1),
		exited: make(chan error, 1), stderr: stderr,
	}
	go func() {
		session.exited <- command.Wait()
		close(session.exited)
	}()
	go readCDPResponses(responseRead, session.responses, session.failures)
	return session, nil
}

func (session *nativeBrowser) extensions(ctx context.Context) ([]nativeExtension, error) {
	session.nextID++
	id := session.nextID
	if err := writeCDPRequest(session.request, id, "Extensions.getExtensions", nil); err != nil {
		return nil, err
	}
	response, err := awaitCDPResponse(ctx, id, session.responses, session.failures, session.exited, session.stderr)
	if err != nil {
		return nil, err
	}
	var result struct {
		Extensions []nativeExtension `json:"extensions"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return nil, fmt.Errorf("invalid browser extension inventory: %w", err)
	}
	return result.Extensions, nil
}

func (session *nativeBrowser) waitForPath(ctx context.Context, path string) (nativeExtension, error) {
	return session.waitForPathWithTimeout(ctx, path, 0)
}

func (session *nativeBrowser) waitForPathWithTimeout(ctx context.Context, path string, timeout time.Duration) (nativeExtension, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	for {
		requestContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		items, err := session.extensions(requestContext)
		cancel()
		if err != nil {
			return nativeExtension{}, err
		}
		for _, item := range items {
			if sameExtensionPath(item.Path, path) && item.Enabled && item.ID != "" {
				return item, nil
			}
		}
		select {
		case <-ctx.Done():
			return nativeExtension{}, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func sameExtensionPath(left, right string) bool {
	if !filepath.IsAbs(left) || !filepath.IsAbs(right) {
		return false
	}
	for _, value := range []*string{&left, &right} {
		if resolved, err := filepath.EvalSymlinks(*value); err == nil {
			*value = resolved
		}
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func (session *nativeBrowser) close() error {
	if session == nil {
		return nil
	}
	session.nextID++
	_ = writeCDPRequest(session.request, session.nextID, "Browser.close", nil)
	select {
	case <-session.exited:
		return nil
	case <-time.After(10 * time.Second):
		return errors.New("browser did not close after installation")
	}
}

func (session *nativeBrowser) kill() {
	if session == nil {
		return
	}
	_ = session.command.Process.Kill()
	select {
	case <-session.exited:
	case <-time.After(5 * time.Second):
	}
	_ = session.request.Close()
	_ = session.response.Close()
}
