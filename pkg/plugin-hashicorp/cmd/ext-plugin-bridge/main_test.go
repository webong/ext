package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin-hashicorp"
	"github.com/webong/ext/pkg/plugin/jsonline"
	"github.com/webong/ext/pkg/plugin/plugintest"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestConfiguration(t *testing.T) {
	c := config{APIVersion: "ext.bridge/v1", Descriptor: plugintest.Descriptor(), Process: hashicorp.Process{Executable: "/tmp/guest", SHA256: hex.EncodeToString(make([]byte, 32)), Protocol: "grpc"}, Allow: []permission{{Contract: plugintest.Descriptor().Contracts[0].ContractRef, Operation: "echo"}}}
	for _, bad := range []string{"", "wrong"} {
		c.APIVersion = bad
		b, _ := json.Marshal(c)
		p := filepath.Join(t.TempDir(), "config.json")
		_ = os.WriteFile(p, b, 0600)
		if _, err := load(p); err == nil {
			t.Fatal("accepted unsupported config")
		}
	}
	c.APIVersion = "ext.bridge/v1"
	if err := c.authorize(context.Background(), plugin.Request{Contract: c.Allow[0].Contract, Operation: "wait"}); !errors.Is(err, plugin.ErrDenied) {
		t.Fatal(err)
	}
}
func artifact(t *testing.T, name string) string {
	t.Helper()
	p := os.Getenv(name)
	if p == "" {
		if os.Getenv("EXT_BRIDGE_REQUIRED") == "1" {
			t.Fatalf("missing %s", name)
		}
		t.Skip("run scripts/plugin-interop.sh")
	}
	return p
}
func selection(t *testing.T, protocol string) (config, string) {
	t.Helper()
	guest := artifact(t, "EXT_BRIDGE_GUEST")
	b, err := os.ReadFile(guest)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	d := plugintest.Descriptor()
	c := config{APIVersion: "ext.bridge/v1", Descriptor: d, Process: hashicorp.Process{Executable: guest, SHA256: hex.EncodeToString(sum[:]), Protocol: protocol, Arguments: []string{"--protocol", protocol}}}
	for _, op := range d.Contracts[0].Operations {
		c.Allow = append(c.Allow, permission{Contract: d.Contracts[0].ContractRef, Operation: op.Name})
	}
	return c, artifact(t, "EXT_BRIDGE_EXECUTABLE")
}
func configured(t *testing.T, c config) string {
	t.Helper()
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bridge.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

type processConn struct {
	io.ReadCloser
	io.WriteCloser
	cmd  *exec.Cmd
	once sync.Once
}

func (c *processConn) Close() error {
	c.once.Do(func() {
		_ = c.WriteCloser.Close()
		_ = c.ReadCloser.Close()
		done := make(chan struct{})
		go func() { _ = c.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = c.cmd.Process.Kill()
			<-done
		}
	})
	return nil
}
func connect(t *testing.T, exe, path string) (*jsonline.Client, *exec.Cmd) {
	t.Helper()
	cmd := exec.Command(exe, "--config", path)
	cmd.Env = []string{}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := jsonline.NewClient(&processConn{ReadCloser: out, WriteCloser: in, cmd: cmd})
	t.Cleanup(func() { _ = c.Close() })
	return c, cmd
}
func TestBridgeConformance(t *testing.T) {
	for _, protocol := range []string{"grpc", "netrpc"} {
		t.Run(protocol, func(t *testing.T) {
			c, exe := selection(t, protocol)
			path := configured(t, c)
			plugintest.Run(t, func(context.Context) (plugin.Backend, error) { b, _ := connect(t, exe, path); return b, nil })
		})
	}
}
func TestBridgeDenialAndPin(t *testing.T) {
	c, exe := selection(t, "grpc")
	c.Process.SHA256 = hex.EncodeToString(make([]byte, 32))
	b, _ := connect(t, exe, configured(t, c))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := b.Handshake(ctx); err == nil {
		t.Fatal("wrong digest admitted")
	}
	c, exe = selection(t, "grpc")
	c.Allow = c.Allow[:1]
	b, _ = connect(t, exe, configured(t, c))
	d, err := b.Handshake(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := plugin.Request{APIVersion: plugin.APIVersion, ID: "outside", Plugin: d.Identity, Contract: d.Contracts[0].ContractRef, Operation: "wait", Deadline: time.Now().Add(time.Second)}
	out, err := b.Invoke(ctx, r)
	if err != nil || out.Error == nil || out.Error.Code != "permission_denied" {
		t.Fatalf("%+v %v", out, err)
	}
	r.Operation = "echo"
	r.Payload = json.RawMessage(`{"value":7}`)
	out, err = b.Invoke(ctx, r)
	if err != nil || out.ID != "outside" || string(out.Payload) != `{"value":7}` {
		t.Fatalf("%+v %v", out, err)
	}
}
func TestReverseHashicorpFrontend(t *testing.T) {
	guest := artifact(t, "EXT_BRIDGE_JSONLINE_GUEST")
	guestData, err := os.ReadFile(guest)
	if err != nil {
		t.Fatal(err)
	}
	guestSum := sha256.Sum256(guestData)
	exe := artifact(t, "EXT_BRIDGE_EXECUTABLE")
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	for _, protocol := range []string{"grpc", "netrpc"} {
		t.Run(protocol, func(t *testing.T) {
			c, _ := selection(t, protocol)
			c.Process = hashicorp.Process{}
			c.Frontend = "hashicorp-" + protocol
			c.JSONLine = &jsonlineProcess{Executable: guest, SHA256: hex.EncodeToString(guestSum[:])}
			process := hashicorp.Process{Executable: exe, SHA256: hex.EncodeToString(sum[:]), Protocol: protocol, Arguments: []string{"--config", configured(t, c)}}
			plugintest.Run(t, func(ctx context.Context) (plugin.Backend, error) { return hashicorp.ConnectProcess(ctx, process) })
		})
	}
}
