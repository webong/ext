//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/host/process"
	"github.com/webong/ext/pkg/plugin-go/contract/plugintest/enginetest"
)

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
	why      string
	goOpen   string
	goEvents []string
	cOpen    string
	cEvents  []string
}

// stageEvent is the line eventLog keeps for a stage of the test plugin.
func stageEvent(stage, code string) string {
	return fmt.Sprintf("%s id=cengine/progress/r1 contract=/ op=%q request=%q code=%s", stage, "", "", code)
}

func (d *deliberateDifference) outcome(c bool) string {
	open, events := d.goOpen, d.goEvents
	if c {
		open, events = d.cOpen, d.cEvents
	}
	return "open:" + open + "\n  " + strings.Join(events, "\n  ")
}

// TestProcessObserverEventsMatchGo opens a child process through the pure-Go session (process.Start
// as the connect step) and through the C engine, which owns the child, and requires the same
// events and the same error classes for every way admitting it can fail, and for a call that finds
// the child gone.
func TestProcessObserverEventsMatchGo(t *testing.T) {
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
			cOpen: "mismatch,", cEvents: []string{stageEvent("verify", "ok"), stageEvent("connect", "ok"), stageEvent("handshake", "mismatch")}}},
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
			cOpen: "invalid,", cEvents: nil}},
		{name: "verify-denied", mode: "go", denied: true},
		{name: "canceled-context", mode: "go", cancel: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := func(open func(context.Context, string, plugin.Options) (progressSession, error)) string {
				exe := c.exe
				if exe == "" {
					exe = guestPath(t)
				}
				rec := &enginetest.EventLog{}
				o := progressOptions()
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
				out := "open:" + causes(err)
				if err == nil {
					defer s.Abort()
					if c.call {
						_, err = s.CallProgress(context.Background(), progressRef, "plain", nil, func(plugin.Progress) {})
						out += " call:" + causes(err)
					}
				}
				return out + "\n  " + rec.Text()
			}
			goOut := run(func(ctx context.Context, exe string, o plugin.Options) (progressSession, error) {
				o.Connect = func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
					p, err := process.Start(ctx, process.Command{Path: exe, Env: []string{guestEnv + "=" + c.mode}})
					if err != nil {
						return nil, err
					}
					return p, nil
				}
				s, err := plugin.Open(ctx, progressDescriptor(), o)
				if err != nil {
					return nil, err
				}
				return s, nil
			})
			cOut := run(func(ctx context.Context, exe string, o plugin.Options) (progressSession, error) {
				launch := LaunchOptions{ProcessOptions: ProcessOptions{Executable: exe, Environment: []string{guestEnv + "=" + c.mode}}}
				s, err := OpenProcess(ctx, progressDescriptor(), launch, o)
				if err != nil {
					return nil, err
				}
				return s, nil
			})
			t.Logf("events:\n%s", goOut)
			if c.differs != nil {
				if goOut != c.differs.outcome(false) || cOut != c.differs.outcome(true) {
					t.Fatalf("%s: expected the deliberate difference\n go:\n%s\n c:\n%s\n want go:\n%s\n want c:\n%s", c.differs.why, goOut, cOut, c.differs.outcome(false), c.differs.outcome(true))
				}
				return
			}
			if goOut != cOut {
				t.Fatalf("process observer events differ\n go:\n%s\n c:\n%s", goOut, cOut)
			}
		})
	}
}
