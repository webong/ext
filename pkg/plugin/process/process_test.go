package process_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/jsonline"
	"github.com/webong/ext/pkg/plugin/process"
)

var descriptor = plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/p", Revision: "r1"},
	Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.echo", Version: "v1"}, Operations: []plugin.Operation{{Name: "echo"}, {Name: "hang"}, {Name: "busy"}, {Name: "env"}, {Name: "big"}}}}}

func TestMain(m *testing.M) {
	if os.Getenv("PROCESS_TEST_CHILD") == "1" {
		fmt.Fprint(os.Stderr, strings.Repeat("e", 10000))
		os.Exit(serve())
	}
	os.Exit(m.Run())
}

func serve() int {
	g, err := plugin.NewGuest(descriptor, plugin.GuestOptions{Handler: func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
		switch r.Operation {
		case "hang":
			<-ctx.Done()
			return nil, ctx.Err()
		case "busy":
			return nil, &plugin.RemoteError{Code: "busy", Message: "later", RetryAfterMilliseconds: 250}
		case "env":
			return json.Marshal(os.Environ())
		case "big":
			return json.Marshal(strings.Repeat("x", plugin.MaxFrameBytes))
		}
		return r.Payload, nil
	}})
	if err != nil {
		return 2
	}
	if err := jsonline.ServeStdio(context.Background(), g); err != nil {
		return 1
	}
	return 0
}

func start(t *testing.T, env ...string) *process.Process {
	t.Helper()
	exe, _ := os.Executable()
	p, err := process.Start(context.Background(), process.Command{Path: exe, Env: append([]string{"PROCESS_TEST_CHILD=1"}, env...)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func open(t *testing.T, p *process.Process) *plugin.Session {
	t.Helper()
	s, err := plugin.Open(context.Background(), descriptor, plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return nil },
		Authorize: func(context.Context, plugin.Request) error { return nil },
		Connect:   func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return p, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func call(s *plugin.Session, op string, timeout time.Duration) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return s.Call(ctx, descriptor.Contracts[0].ContractRef, op, json.RawMessage(`"hi"`))
}

func TestEchoStderrAndScrubbedEnv(t *testing.T) {
	t.Setenv("CYM_SECRET_TEST", "leak")
	p := start(t, "EXPLICIT=1")
	s := open(t, p)
	r, err := call(s, "echo", 5*time.Second)
	if err != nil || string(r) != `"hi"` {
		t.Fatal(r, err)
	}
	r, err = call(s, "env", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var env []string
	json.Unmarshal(r, &env)
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "CYM_SECRET_TEST") || !strings.Contains(joined, "EXPLICIT=1") || !strings.Contains(joined, "PATH=") {
		t.Fatal(joined)
	}
	if n := len(p.Stderr()); n != process.DefaultStderrBytes {
		t.Fatalf("stderr %d", n)
	}
}

func TestRemoteErrorRetryAfter(t *testing.T) {
	s := open(t, start(t))
	_, err := call(s, "busy", 5*time.Second)
	var remote *plugin.RemoteError
	if !errors.As(err, &remote) || remote.Code != "busy" || remote.RetryAfterMilliseconds != 250 {
		t.Fatal(err)
	}
	if _, err := call(s, "echo", 5*time.Second); err != nil {
		t.Fatal("structured error must leave process usable:", err)
	}
}

func TestCancelKillsAndForbidsReuse(t *testing.T) {
	p := start(t)
	s := open(t, p)
	if _, err := call(s, "hang", 300*time.Millisecond); err == nil {
		t.Fatal("hang returned")
	}
	if _, err := call(s, "echo", 5*time.Second); err == nil {
		t.Fatal("process reused after cancel")
	}
	if _, err := p.Invoke(context.Background(), plugin.Request{}); !errors.Is(err, plugin.ErrClosed) {
		t.Fatal(err)
	}
}

func TestFrameLimit(t *testing.T) {
	s := open(t, start(t))
	if _, err := call(s, "big", 10*time.Second); err == nil {
		t.Fatal("oversize response accepted")
	}
}

func TestStartRejectsBadCommand(t *testing.T) {
	ctx := context.Background()
	for _, c := range []process.Command{{}, {Path: t.TempDir()}, {Path: "/nonexistent/x"}, {Path: os.Args[0], Env: []string{"NOEQUALS"}}} {
		if p, err := process.Start(ctx, c); err == nil {
			p.Close()
			t.Fatalf("accepted %+v", c)
		}
	}
}
