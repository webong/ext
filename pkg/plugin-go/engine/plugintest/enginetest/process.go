package enginetest

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"github.com/webong/ext/pkg/plugin-go/engine/host/process"
	"github.com/webong/ext/pkg/plugin-go/engine/transport/jsonline"
)

// GuestEnv names the mode in which a test binary serves as a plugin instead of running its tests.
// The process checks start the binary they run in as the child, with this variable set; an engine's
// TestMain calls MaybeServeGuest first.
const GuestEnv = "ENGINETEST_GUEST"

// MaybeServeGuest runs the plugin the environment asks for and exits, when GuestEnv is set. Call it
// at the start of TestMain.
func MaybeServeGuest() {
	if mode := os.Getenv(GuestEnv); mode != "" {
		os.Exit(runGuest(mode))
	}
}

var guestRef = plugin.ContractRef{Name: "progress.test", Version: "v1"}

func guestDescriptor() plugin.Descriptor {
	return plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "cgoengine/progress", Revision: "r1"},
		Contracts: []plugin.Contract{{ContractRef: guestRef, Operations: []plugin.Operation{
			{Name: "plain"}, {Name: "stream", Progress: true}, {Name: "flood", Progress: true},
			{Name: "fail", Progress: true}, {Name: "limits", Progress: true}, {Name: "plainlimits"},
			{Name: "flooddrops", Progress: true},
		}}},
	}
}

func runGuest(mode string) int {
	if strings.HasPrefix(mode, "stage-") {
		return runStageGuest(mode)
	}
	guest, err := plugin.NewGuest(guestDescriptor(), plugin.GuestOptions{Handler: func(context.Context, plugin.Request) (json.RawMessage, error) {
		return json.RawMessage(`{"done":true}`), nil
	}})
	if err != nil {
		return 2
	}
	if err := jsonline.ServeStdio(context.Background(), guest); err != nil {
		return 1
	}
	return 0
}

// runStageGuest is a guest that fails a stage of admission in the way its mode names, written by
// hand: it exits at once, answers the handshake with something else, or never answers it.
func runStageGuest(mode string) int {
	if mode == "stage-exit" {
		return 3
	}
	in := bufio.NewReader(os.Stdin)
	if _, err := in.ReadString('\n'); err != nil {
		return 0
	}
	descriptor := guestDescriptor()
	hello := func(d plugin.Descriptor) {
		data, _ := json.Marshal(d)
		fmt.Printf(`{"apiVersion":"ext.plugin/v1","id":"hello","payload":%s}`+"\n", data)
	}
	switch mode {
	case "stage-silent":
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	case "stage-garbage":
		fmt.Print("this is not json\n")
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "stage-wrong-revision":
		descriptor.Identity.Revision = "other"
		hello(descriptor)
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "stage-other-contract":
		descriptor.Contracts = descriptor.Contracts[:1]
		descriptor.Contracts[0].Operations = descriptor.Contracts[0].Operations[:1]
		hello(descriptor)
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "stage-error-hello":
		fmt.Print(`{"apiVersion":"ext.plugin/v1","id":"hello","error":{"code":"busy","message":"later"}}` + "\n")
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "stage-wrong-id":
		data, _ := json.Marshal(descriptor)
		fmt.Printf(`{"apiVersion":"ext.plugin/v1","id":"other","payload":%s}`+"\n", data)
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "stage-exit-on-call":
		hello(descriptor)
		_, _ = in.ReadString('\n')
		return 4
	default:
		return 2
	}
	return 0
}

// Launch is what an engine starts as the plugin's child process.
type Launch struct {
	Executable  string
	Environment []string
}

func guestOptions() plugin.Options {
	return plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return nil },
		Authorize: func(context.Context, plugin.Request) error { return nil },
		Timeout:   20 * time.Second,
	}
}

func executable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

type processCase struct {
	name string
	mode string
	// exe is the executable to start; empty is this test binary, which serves the mode.
	exe    string
	denied bool
	// cancel opens with a context that is already canceled.
	cancel  bool
	timeout time.Duration
	// call makes one call once the session is open.
	call bool
	// differs, when set, names a difference between the two that is deliberate, and what each
	// reports: the comparison then requires exactly those two outcomes instead of equal ones.
	differs *deliberateDifference
}

type deliberateDifference struct {
	why          string
	goOpen       string
	goEvents     []string
	engineOpen   string
	engineEvents []string
}

// stageEvent is the line eventLog keeps for a stage of the test plugin.
func stageEvent(stage, code string) string {
	return fmt.Sprintf("%s id=cgoengine/progress/r1 contract=/ op=%q request=%q code=%s", stage, "", "", code)
}

func (d *deliberateDifference) outcome(engine bool) string {
	open, events := d.goOpen, d.goEvents
	if engine {
		open, events = d.engineOpen, d.engineEvents
	}
	return "open:" + open + "\n  " + strings.Join(events, "\n  ")
}

// ProcessObserverEventsMatchGo opens a child process through the pure-Go session (process.Start
// as the connect step) and through the engine, which owns the child, and requires the same
// events and the same error classes for every way admitting it can fail, and for a call that finds
// the child gone.
func ProcessObserverEventsMatchGo(t *testing.T, e Engine) {
	if e.OpenProcess == nil {
		t.Skip("the engine launches no processes")
	}
	notExecutable := filepath.Join(t.TempDir(), "plain.txt")
	if err := os.WriteFile(notExecutable, []byte("not a program"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	cases := []processCase{
		{name: "ok", mode: "go", call: true},
		{name: "exit-at-once", mode: "stage-exit"},
		{name: "garbage-hello", mode: "stage-garbage"},
		{name: "wrong-revision", mode: "stage-wrong-revision"},
		{name: "other-contract", mode: "stage-other-contract"},
		// Go returns the plugin's own error from the handshake; the engine has no way to carry a
		// plugin's code out of a start, and reports that the plugin is not the one selected.
		{name: "error-hello", mode: "stage-error-hello", differs: &deliberateDifference{
			why:    "a plugin's error at the handshake",
			goOpen: "remote:busy,", goEvents: []string{stageEvent("verify", "ok"), stageEvent("connect", "ok"), stageEvent("handshake", "busy")},
			engineOpen: "mismatch,", engineEvents: []string{stageEvent("verify", "ok"), stageEvent("connect", "ok"), stageEvent("handshake", "mismatch")}}},
		{name: "wrong-hello-id", mode: "stage-wrong-id"},
		{name: "silent-until-timeout", mode: "stage-silent", timeout: 300 * time.Millisecond},
		{name: "exit-on-call", mode: "stage-exit-on-call", call: true},
		{name: "missing-executable", exe: missing},
		{name: "not-executable", exe: notExecutable},
		// The engine refuses a launch it cannot make before any stage runs, so there are no events;
		// Go tries to start it and fails at the connect stage.
		{name: "relative-executable", exe: "relative", differs: &deliberateDifference{
			why:    "a launch the engine refuses as malformed",
			goOpen: "other", goEvents: []string{stageEvent("verify", "ok"), stageEvent("connect", "failed")},
			engineOpen: "invalid,", engineEvents: nil}},
		{name: "verify-denied", mode: "go", denied: true},
		{name: "canceled-context", mode: "go", cancel: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := func(open func(context.Context, string, plugin.Options) (Session, error)) string {
				exe := c.exe
				if exe == "" {
					exe = executable(t)
				}
				rec := &EventLog{}
				o := guestOptions()
				o.Observer = rec.Observe
				if c.timeout > 0 {
					o.Timeout = c.timeout
				}
				if c.denied {
					o.Verify = func(context.Context, plugin.Descriptor) error { return plugin.ErrDenied }
				}
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				if c.cancel {
					cancelled, stop := context.WithCancel(ctx)
					stop()
					ctx = cancelled
				}
				s, err := open(ctx, exe, o)
				out := "open:" + Causes(err)
				if err == nil {
					defer s.Abort()
					if c.call {
						_, err = s.Call(context.Background(), guestRef, "plain", nil)
						out += " call:" + Causes(err)
					}
				}
				return out + "\n  " + rec.Text()
			}
			goOut := run(func(ctx context.Context, exe string, o plugin.Options) (Session, error) {
				o.Connect = func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
					p, err := process.Start(ctx, process.Command{Path: exe, Env: []string{GuestEnv + "=" + c.mode}})
					if err != nil {
						return nil, err
					}
					return p, nil
				}
				s, err := plugin.Open(ctx, guestDescriptor(), o)
				if err != nil {
					return nil, err
				}
				return s, nil
			})
			engineOut := run(func(ctx context.Context, exe string, o plugin.Options) (Session, error) {
				s, err := e.OpenProcess(ctx, guestDescriptor(), Launch{Executable: exe, Environment: []string{GuestEnv + "=" + c.mode}}, o)
				if err != nil {
					return nil, err
				}
				return s, nil
			})
			t.Logf("events:\n%s", goOut)
			if c.differs != nil {
				if goOut != c.differs.outcome(false) || engineOut != c.differs.outcome(true) {
					t.Fatalf("%s: expected the deliberate difference\n go:\n%s\n engine:\n%s\n want go:\n%s\n want engine:\n%s", c.differs.why, goOut, engineOut, c.differs.outcome(false), c.differs.outcome(true))
				}
				return
			}
			if goOut != engineOut {
				t.Fatalf("process observer events differ\n go:\n%s\n engine:\n%s", goOut, engineOut)
			}
		})
	}
}
