// ctx-plugin-bridge exposes a reviewed HashiCorp-backed endpoint over CTX JSON
// lines. Configuration is supplied by the embedding application's trust policy.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"

	"github.com/hashicorp/go-hclog"
	hc "github.com/hashicorp/go-plugin"
	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin-hashicorp"
	"github.com/webong/ctx/pkg/plugin/bridge"
	"github.com/webong/ctx/pkg/plugin/jsonline"
)

type permission struct {
	Identity  *plugin.Identity   `json:"identity,omitempty"`
	Contract  plugin.ContractRef `json:"contract"`
	Operation string             `json:"operation"`
}
type config struct {
	Frontend   string            `json:"frontend,omitempty"`
	JSONLine   *jsonlineProcess  `json:"jsonline,omitempty"`
	APIVersion string            `json:"apiVersion"`
	Descriptor plugin.Descriptor `json:"descriptor"`
	Process    hashicorp.Process `json:"process"`
	Allow      []permission      `json:"allow"`
}

func load(path string) (config, error) {
	f, err := os.Open(path)
	if err != nil {
		return config{}, err
	}
	defer f.Close()
	return decode(f)
}
func decode(reader io.Reader) (config, error) {
	var c config
	data, err := io.ReadAll(io.LimitReader(reader, plugin.MaxFrameBytes+1))
	if err == nil {
		err = plugin.Decode(data, &c)
	}
	if err != nil {
		return c, err
	}
	if c.APIVersion != "ctx.bridge/v1" || len(c.Allow) == 0 || len(c.Allow) > 16384 {
		return c, plugin.ErrInvalid
	}
	requirements := make([]plugin.Requirement, len(c.Allow))
	for i, a := range c.Allow {
		requirements[i] = plugin.Requirement(a)
	}
	if err = plugin.CheckRequirements(c.Descriptor, requirements); err != nil {
		return c, err
	}
	if c.Frontend != "" && c.Frontend != "jsonline" && c.Frontend != "hashicorp-grpc" && c.Frontend != "hashicorp-netrpc" {
		return c, plugin.ErrUnsupported
	}
	if c.JSONLine != nil {
		if c.Process.Executable != "" || c.Process.Protocol != "" || c.Process.SHA256 != "" || len(c.Process.Arguments) > 0 || len(c.Process.Environment) > 0 {
			return c, plugin.ErrInvalid
		}
		err = c.JSONLine.Validate()
	} else {
		err = c.Process.Validate()
	}
	if err != nil {
		return c, err
	}
	return c, nil
}
func (c config) authorize(_ context.Context, r plugin.Request) error {
	for _, a := range c.Allow {
		if a.Contract == r.Contract && a.Operation == r.Operation && (a.Identity == nil || *a.Identity == r.Plugin) {
			return nil
		}
	}
	return plugin.ErrDenied
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		// Errors can originate in native runtimes; don't dump guest payloads or config.
		fmt.Fprintln(os.Stderr, "ctx-plugin-bridge: stopped:", safeError(err))
		os.Exit(1)
	}
}
func safeError(err error) string {
	for _, e := range []error{plugin.ErrInvalid, plugin.ErrDenied, plugin.ErrMismatch, plugin.ErrUnsupported, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return "transport or configuration failure"
}
func run(args []string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("ctx-plugin-bridge", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	file := flags.String("config", self+".json", "reviewed bridge configuration (defaults to executable.json)")
	watch := flags.Bool("watch-parent", false, "internal parent-liveness supervisor (fd 3)")
	if err = flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return plugin.ErrInvalid
	}
	path, err := filepath.Abs(*file)
	if err != nil {
		return err
	}
	if *watch {
		snapshot := os.NewFile(4, "ctx-bridge-selection")
		if snapshot == nil {
			return plugin.ErrInvalid
		}
		c, err := decode(snapshot)
		closeErr := snapshot.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		return watchParent(c)
	}
	c, err := load(path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if c.Frontend == "hashicorp-grpc" || c.Frontend == "hashicorp-netrpc" {
		return serveHashicorp(ctx, self, c)
	}
	return launchWorker(ctx, self, c)
}

// The worker (not its launcher) owns go-plugin's client and the actual guest.
// This separates parent-liveness supervision from go-plugin's process killing.
func serve(ctx context.Context, c config) error {
	relay, err := openRelay(ctx, c.Descriptor, plugin.Options{
		Verify: func(context.Context, plugin.Descriptor) error {
			if c.JSONLine != nil {
				return c.JSONLine.Verify()
			}
			return c.Process.Verify()
		},
		Authorize: c.authorize,
		Connect: func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
			if c.JSONLine != nil {
				return c.JSONLine.Connect(ctx)
			}
			return hashicorp.ConnectProcess(ctx, c.Process)
		},
	})
	if err != nil {
		return err
	}
	return bridge.ServeJSONLine(ctx, stdio{}, relay)
}
func launchWorker(ctx context.Context, self string, c config) error {
	lifeRead, lifeWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer lifeWrite.Close()
	configRead, configWrite, err := os.Pipe()
	if err != nil {
		_ = lifeRead.Close()
		return err
	}
	command := exec.Command(self, "--watch-parent")
	command.Env = []string{}
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.ExtraFiles = []*os.File{lifeRead, configRead}
	if err = command.Start(); err != nil {
		_ = lifeRead.Close()
		_ = configRead.Close()
		_ = configWrite.Close()
		return err
	}
	_ = lifeRead.Close()
	_ = configRead.Close()
	// The config snapshot is read before worker verification/startup, so no second
	// file read can change the reviewed launch selection.
	writeErr := json.NewEncoder(configWrite).Encode(c)
	_ = configWrite.Close()
	if writeErr != nil {
		_ = lifeWrite.Close()
		_ = command.Wait()
		return writeErr
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	select {
	case err := <-exited:
		return err
	case <-ctx.Done():
		_ = lifeWrite.Close()
		<-exited
		return ctx.Err()
	}
}

type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return errors.Join(os.Stdin.Close(), os.Stdout.Close()) }

// The frontend may be killed directly by go-plugin. A private liveness pipe
// lets its worker survive just long enough to close and reap the actual guest.
func serveHashicorp(ctx context.Context, self string, c config) error {
	relay, err := openRelay(ctx, c.Descriptor, plugin.Options{
		Verify: func(context.Context, plugin.Descriptor) error {
			if c.JSONLine != nil {
				return c.JSONLine.Verify()
			}
			return c.Process.Verify()
		},
		Authorize: c.authorize,
		Connect: func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
			return connectWorker(ctx, self, c)
		},
	})
	if err != nil {
		return err
	}
	defer relay.Close()
	stop := context.AfterFunc(ctx, func() { _ = relay.Close() })
	defer stop()
	options := &hc.ServeConfig{HandshakeConfig: hashicorp.HandshakeConfig(), Plugins: hc.PluginSet{hashicorp.PluginName: &hashicorp.Plugin{Guest: relay}}, Logger: hclog.NewNullLogger()}
	if c.Frontend == "hashicorp-grpc" {
		options.GRPCServer = hashicorp.GRPCServer
	}
	hc.Serve(options)
	return nil
}
func connectWorker(ctx context.Context, self string, c config) (plugin.Backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lifeRead, lifeWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	configRead, configWrite, err := os.Pipe()
	if err != nil {
		_ = lifeRead.Close()
		_ = lifeWrite.Close()
		return nil, err
	}
	cmd := exec.Command(self, "--watch-parent")
	cmd.Env = []string{}
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{lifeRead, configRead}
	conn, err := startPiped(cmd)
	_ = lifeRead.Close()
	_ = configRead.Close()
	if err != nil {
		_ = lifeWrite.Close()
		_ = configWrite.Close()
		return nil, err
	}
	conn.life = lifeWrite
	writeErr := json.NewEncoder(configWrite).Encode(c)
	_ = configWrite.Close()
	if writeErr != nil {
		_ = conn.Close()
		return nil, writeErr
	}
	if err = ctx.Err(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return jsonline.NewClient(conn), nil
}
