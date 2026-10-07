package firefox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/webong/ext/res/web/extension"
)

// InstallExtension uses Firefox's WebDriver BiDi permanent-install command.
// Firefox checks the XPI signature; the adapter verifies the same enabled ID
// after restarting the caller-selected profile.
func InstallExtension(ctx context.Context, browserTarget extension.BrowserTarget, source, expectedRevision string, report func(extension.InstallResult) error) error {
	target := browserTarget
	if target.Browser != "firefox" {
		return errors.New("Firefox installation requires a visible Firefox target")
	}
	if !filepath.IsAbs(target.ExecutablePath) || !filepath.IsAbs(target.ProfilePath) || !filepath.IsAbs(source) {
		return errors.New("Firefox executable, profile, and XPI paths must be absolute")
	}
	if !strings.EqualFold(filepath.Ext(source), ".xpi") || expectedRevision == "" || report == nil {
		return errors.New("Firefox installation requires a signed XPI, prepared revision, and status callback")
	}
	info, err := os.Stat(target.ExecutablePath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) {
		return errors.New("Firefox executable path is not executable")
	}
	description, err := extension.Inspect(source)
	if err != nil {
		return err
	}
	if description.Revision != expectedRevision {
		return errors.New("Firefox XPI changed since inspection")
	}
	if err := os.MkdirAll(target.ProfilePath, 0700); err != nil {
		return err
	}
	first, err := startFirefox(ctx, target)
	if err != nil {
		return err
	}
	defer first.stop()
	var installed struct {
		Extension string `json:"extension"`
	}
	if err := first.call(ctx, "webExtension.install", map[string]any{
		"extensionData": map[string]string{"type": "archivePath", "path": source},
		"moz:permanent": true,
	}, &installed); err != nil {
		return fmt.Errorf("Firefox rejected permanent XPI installation: %w", err)
	}
	if installed.Extension == "" {
		return errors.New("Firefox did not return an extension ID")
	}
	if err := first.verify(ctx, target.ProfilePath, installed.Extension, description, false); err != nil {
		return err
	}
	if err := first.close(ctx); err != nil {
		return fmt.Errorf("close Firefox for verification: %w", err)
	}
	second, err := startFirefox(ctx, target)
	if err != nil {
		return fmt.Errorf("restart Firefox for verification: %w", err)
	}
	defer second.stop()
	if err := second.verify(ctx, target.ProfilePath, installed.Extension, description, true); err != nil {
		return fmt.Errorf("Firefox extension did not survive restart: %w", err)
	}
	current, err := extension.Inspect(source)
	if err != nil || current.Revision != expectedRevision {
		return errors.New("Firefox XPI changed during installation")
	}
	if err := report(extension.InstallResult{Status: "installed", Browser: "firefox", ID: installed.Extension,
		Source: source, Profile: target.ProfilePath, Extension: &description,
		NextAction: "Firefox accepted the signed XPI and kept it enabled after restart."}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return second.close(context.Background())
	case <-second.exited:
		return nil
	}
}

// ActivateExtension loads a signed XPI as a temporary WebDriver BiDi extension.
// The extension is removed when the controlled Firefox session closes.
func ActivateExtension(ctx context.Context, browserTarget extension.BrowserTarget, source, expectedRevision string, ready func(extension.InstallResult) error) error {
	target := browserTarget
	if target.Browser != "firefox" {
		return errors.New("Firefox session activation requires a visible Firefox target")
	}
	if !filepath.IsAbs(target.ExecutablePath) || !filepath.IsAbs(target.ProfilePath) || !filepath.IsAbs(source) {
		return errors.New("Firefox executable, profile, and XPI paths must be absolute")
	}
	if !strings.EqualFold(filepath.Ext(source), ".xpi") || expectedRevision == "" || ready == nil {
		return errors.New("Firefox activation requires a signed XPI, prepared revision, and ready callback")
	}
	info, err := os.Stat(target.ExecutablePath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) {
		return errors.New("Firefox executable path is not executable")
	}
	description, err := extension.Inspect(source)
	if err != nil {
		return err
	}
	if description.Revision != expectedRevision {
		return errors.New("Firefox XPI changed since inspection")
	}
	if err := os.MkdirAll(target.ProfilePath, 0700); err != nil {
		return err
	}
	session, err := startFirefox(ctx, target)
	if err != nil {
		return err
	}
	defer session.stop()
	var installed struct {
		Extension string `json:"extension"`
	}
	if err := session.call(ctx, "webExtension.install", map[string]any{
		"extensionData": map[string]string{"type": "archivePath", "path": source},
		"moz:permanent": false,
	}, &installed); err != nil {
		return fmt.Errorf("Firefox rejected temporary XPI activation: %w", err)
	}
	if installed.Extension == "" {
		return errors.New("Firefox did not return a temporary extension ID")
	}
	result := extension.InstallResult{Status: "activated", Browser: "firefox", ID: installed.Extension,
		Source: source, Profile: target.ProfilePath, Extension: &description,
		NextAction: "Keep this command running to keep the temporary Firefox extension active."}
	if err := ready(result); err != nil {
		return err
	}
	select {
	case <-session.exited:
		return nil
	case <-ctx.Done():
		return session.close(context.Background())
	}
}

var firefoxEndpoint = regexp.MustCompile(`WebDriver BiDi listening on ws://127\.0\.0\.1:(\d+)`)

type firefoxLog struct {
	mu   sync.Mutex
	text strings.Builder
}

func (log *firefoxLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.text.Len() < 32<<10 {
		_, _ = log.text.Write(data)
	}
	return len(data), nil
}

func (log *firefoxLog) port() int {
	log.mu.Lock()
	defer log.mu.Unlock()
	match := firefoxEndpoint.FindStringSubmatch(log.text.String())
	if len(match) != 2 {
		return 0
	}
	port, _ := strconv.Atoi(match[1])
	return port
}

type firefoxSession struct {
	command *exec.Cmd
	conn    *websocket.Conn
	exited  chan error
	nextID  int
}

func startFirefox(ctx context.Context, target extension.BrowserTarget) (*firefoxSession, error) {
	command := exec.Command(target.ExecutablePath, "--no-remote", "-profile", target.ProfilePath, "--remote-debugging-port", "0", "about:addons")
	log := &firefoxLog{}
	command.Stderr = log
	if err := command.Start(); err != nil {
		return nil, err
	}
	session := &firefoxSession{command: command, exited: make(chan error, 1)}
	go func() { session.exited <- command.Wait() }()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		if port := log.port(); port != 0 {
			endpoint := fmt.Sprintf("ws://127.0.0.1:%d/session", port)
			conn, _, err := websocket.DefaultDialer.DialContext(startup, endpoint, nil)
			if err == nil {
				session.conn = conn
				var created json.RawMessage
				if err := session.call(startup, "session.new", map[string]any{"capabilities": map[string]any{"alwaysMatch": map[string]string{"browserName": "firefox"}}}, &created); err != nil {
					session.stop()
					return nil, err
				}
				return session, nil
			}
		}
		select {
		case err := <-session.exited:
			return nil, fmt.Errorf("Firefox exited before BiDi startup: %v", err)
		case <-startup.Done():
			session.stop()
			return nil, fmt.Errorf("Firefox BiDi startup timed out: %w", startup.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (session *firefoxSession) call(ctx context.Context, method string, params any, result any) error {
	session.nextID++
	id := session.nextID
	deadline := time.Now().Add(30 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = session.conn.SetWriteDeadline(deadline)
	if err := session.conn.WriteJSON(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	_ = session.conn.SetReadDeadline(deadline)
	for {
		var response struct {
			ID      int             `json:"id"`
			Type    string          `json:"type"`
			Result  json.RawMessage `json:"result"`
			Error   string          `json:"error"`
			Message string          `json:"message"`
		}
		if err := session.conn.ReadJSON(&response); err != nil {
			return err
		}
		if response.ID != id {
			continue
		}
		if response.Type == "error" {
			return fmt.Errorf("%s: %s", response.Error, response.Message)
		}
		if response.Type != "success" {
			return errors.New("unexpected Firefox BiDi response")
		}
		return json.Unmarshal(response.Result, result)
	}
}

func (session *firefoxSession) verify(ctx context.Context, profile, id string, expected extension.Description, afterRestart bool) error {
	var inventory struct {
		Extensions []struct {
			ID        string `json:"id"`
			Version   string `json:"version"`
			Active    bool   `json:"isActive"`
			Temporary bool   `json:"temporarilyInstalled"`
		} `json:"extensions"`
	}
	if err := session.call(ctx, "webExtension.moz:listExtensions", map[string]any{}, &inventory); err != nil {
		if !strings.Contains(err.Error(), "unknown command") {
			return err
		}
		// Firefox releases before this BiDi listing command can still prove
		// persistence from the profile metadata written by Firefox on restart.
		if !afterRestart {
			return nil
		}
		verifyContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		for {
			if err := verifyFirefoxProfile(profile, id, expected); err == nil {
				return nil
			}
			select {
			case <-verifyContext.Done():
				return errors.New("Firefox profile did not confirm an active permanent extension after restart")
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	for _, item := range inventory.Extensions {
		if item.ID == id && item.Active && !item.Temporary && item.Version == expected.Version {
			return nil
		}
	}
	return errors.New("Firefox did not report the same active permanent extension")
}

func verifyFirefoxProfile(profile, id string, expected extension.Description) error {
	data, err := os.ReadFile(filepath.Join(profile, "extensions.json"))
	if err != nil {
		return err
	}
	var metadata struct {
		Addons []struct {
			ID           string `json:"id"`
			Version      string `json:"version"`
			Type         string `json:"type"`
			Path         string `json:"path"`
			Active       bool   `json:"active"`
			UserDisabled bool   `json:"userDisabled"`
			AppDisabled  bool   `json:"appDisabled"`
		} `json:"addons"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return err
	}
	for _, item := range metadata.Addons {
		if item.ID != id || item.Type != "extension" || item.Version != expected.Version || !item.Active || item.UserDisabled || item.AppDisabled {
			continue
		}
		relative, err := filepath.Rel(profile, item.Path)
		if err != nil || filepath.Dir(relative) != "extensions" || !strings.EqualFold(filepath.Ext(relative), ".xpi") {
			continue
		}
		actual, err := extension.Inspect(item.Path)
		if err == nil && actual.Revision == expected.Revision {
			return nil
		}
	}
	return errors.New("Firefox profile does not contain the reviewed active extension")
}

func (session *firefoxSession) close(ctx context.Context) error {
	if session.conn != nil {
		var result json.RawMessage
		_ = session.call(ctx, "browser.close", map[string]any{}, &result)
	}
	select {
	case <-session.exited:
		return nil
	case <-time.After(10 * time.Second):
		session.stop()
		return errors.New("Firefox did not exit after browser.close")
	}
}

func (session *firefoxSession) stop() {
	if session == nil {
		return
	}
	if session.conn != nil {
		_ = session.conn.Close()
	}
	if session.command != nil && session.command.Process != nil {
		_ = session.command.Process.Kill()
	}
}
