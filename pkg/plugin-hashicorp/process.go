package hashicorp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hashicorp/go-hclog"
	hc "github.com/hashicorp/go-plugin"
	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/interop"
	"github.com/webong/ctx/pkg/plugin/jsonline"
)

// Process is a reviewed launch selection. SHA256 pins bytes, not publisher
// identity; the application must authenticate this configuration separately.
// Environment is explicit: the parent environment is not inherited.
type Process struct {
	Executable  string   `json:"executable"`
	SHA256      string   `json:"sha256"`
	Protocol    string   `json:"protocol"`
	Arguments   []string `json:"arguments,omitempty"`
	Environment []string `json:"environment,omitempty"`
}

func (p Process) Validate() error {
	digest, err := hex.DecodeString(p.SHA256)
	if err != nil || len(digest) != sha256.Size || !filepath.IsAbs(p.Executable) || strings.ContainsRune(p.Executable, 0) || !utf8.ValidString(p.Executable) || len(p.Executable) > 4096 {
		return plugin.ErrInvalid
	}
	if p.Protocol != "grpc" && p.Protocol != "netrpc" {
		return plugin.ErrUnsupported
	}
	if len(p.Arguments) > 256 || len(p.Environment) > 256 {
		return plugin.ErrInvalid
	}
	for _, arg := range p.Arguments {
		if len(arg) > 4096 || strings.ContainsRune(arg, 0) {
			return plugin.ErrInvalid
		}
	}
	seen := map[string]bool{}
	for _, env := range p.Environment {
		key, _, ok := strings.Cut(env, "=")
		if !ok || key == "" || len(env) > 8192 || strings.ContainsRune(env, 0) || seen[key] {
			return plugin.ErrInvalid
		}
		seen[key] = true
	}
	return nil
}
func (p Process) Verify() error {
	if err := p.Validate(); err != nil {
		return err
	}
	f, err := os.Open(p.Executable)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return plugin.ErrInvalid
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, f)
	closeErr := f.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(p.SHA256) {
		return plugin.ErrMismatch
	}
	return nil
}

// ConnectCommand binds an explicitly supplied command to the selected transport.
// The caller has already verified selection and command/wrapper artifacts. This
// permits lifecycle wrappers without adding product behavior to the library.
// Cmd and its streams become go-plugin owned. Never share it across sessions.
func ConnectCommand(ctx context.Context, command *exec.Cmd, protocol string) (plugin.Backend, error) {
	if command == nil || (protocol != "grpc" && protocol != "netrpc") {
		return nil, plugin.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	timeout := plugin.DefaultTimeout
	if d, ok := ctx.Deadline(); ok && time.Until(d) < timeout {
		timeout = time.Until(d)
	}
	client := hc.NewClient(&hc.ClientConfig{HandshakeConfig: HandshakeConfig(), Plugins: hc.PluginSet{PluginName: &Plugin{}}, AllowedProtocols: []hc.Protocol{hc.Protocol(protocol)}, Cmd: command, SkipHostEnv: true, AutoMTLS: true, StartTimeout: timeout, Logger: hclog.NewNullLogger()})
	return Connect(ctx, client)
}

// ConnectProcess verifies pinned executable content immediately before starting.
// The application still verifies provenance and protects immutable paths against
// replacement. Connect owns cleanup; it is not a process-tree supervisor.
func ConnectProcess(ctx context.Context, p Process) (plugin.Backend, error) {
	if err := p.Verify(); err != nil {
		return nil, err
	}
	command := exec.Command(p.Executable, append([]string(nil), p.Arguments...)...)
	command.Env = append([]string{}, p.Environment...)
	return ConnectCommand(ctx, command, p.Protocol)
}

// JSONLineBridge declares the mechanics of the supplied stdio bridge. Native
// streaming/callbacks do not cross it; declared unary/pull-stream contracts do.
func JSONLineBridge(protocol string) (interop.Bridge, error) {
	var backend plugin.BackendProfile
	switch protocol {
	case "grpc":
		backend = GRPCProfile()
	case "netrpc":
		backend = NetRPCProfile()
	default:
		return interop.Bridge{}, plugin.ErrUnsupported
	}
	return interop.Bridge{Name: "jsonline-to-" + backend.Name, Frontend: jsonline.Profile(), Backend: backend}, nil
}
