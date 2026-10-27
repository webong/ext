package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func baseSpec() Spec {
	return Spec{
		Name: "com.example.agent", Description: "Example agent",
		Executable: "/usr/local/bin/example-agent",
		Arguments:  []string{"--serve", "with space"},
		Environment: map[string]string{
			"EXAMPLE_ROOT": "/var/lib/example",
			"EXAMPLE_MODE": "strict",
		},
		WorkingDirectory: "/var/lib/example",
		Restart:          RestartOnFailure,
		RestartSeconds:   3,
	}
}

func TestRenderLaunchd(t *testing.T) {
	def, err := Render(baseSpec(), PlatformDarwin)
	if err != nil {
		t.Fatal(err)
	}
	if def.Identity != "com.example.agent" || def.FileName != "com.example.agent.plist" {
		t.Fatalf("identity = %q file = %q", def.Identity, def.FileName)
	}
	for _, fragment := range []string{
		"<key>Label</key><string>com.example.agent</string>",
		"<string>/usr/local/bin/example-agent</string><string>--serve</string><string>with space</string>",
		"<key>EXAMPLE_MODE</key><string>strict</string>",
		"<key>SuccessfulExit</key><false/>",
		"<key>ThrottleInterval</key><integer>3</integer>",
		"<key>WorkingDirectory</key><string>/var/lib/example</string>",
	} {
		if !strings.Contains(def.Content, fragment) {
			t.Errorf("launchd definition missing %q:\n%s", fragment, def.Content)
		}
	}
	if def.Digest == "" || len(def.Digest) != 64 {
		t.Fatalf("digest = %q", def.Digest)
	}
}

func TestRenderSystemd(t *testing.T) {
	def, err := Render(baseSpec(), PlatformLinux)
	if err != nil {
		t.Fatal(err)
	}
	if def.Identity != "com.example.agent.service" || def.FileName != def.Identity {
		t.Fatalf("identity = %q file = %q", def.Identity, def.FileName)
	}
	for _, fragment := range []string{
		"Description=Example agent",
		`ExecStart="/usr/local/bin/example-agent" "--serve" "with space"`,
		`Environment="EXAMPLE_MODE=strict"`,
		"Restart=on-failure\nRestartSec=3",
		"WantedBy=default.target",
	} {
		if !strings.Contains(def.Content, fragment) {
			t.Errorf("systemd definition missing %q:\n%s", fragment, def.Content)
		}
	}
	always := baseSpec()
	always.Name = "example.serve"
	always.Restart = RestartAlways
	if def, err = Render(always, PlatformLinux); err != nil || !strings.Contains(def.Content, "Restart=always") {
		t.Fatalf("always restart = %v\n%s", err, def.Content)
	}
}

func TestSystemdEscapesSpecifiersAndDollars(t *testing.T) {
	spec := baseSpec()
	spec.Arguments = []string{"%h", "$HOME", `back\slash`, `quote"d`}
	def, err := Render(spec, PlatformLinux)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(def.Content, `"%h"`) || strings.Contains(def.Content, `"$HOME"`) {
		t.Fatalf("unescaped specifier:\n%s", def.Content)
	}
	for _, fragment := range []string{"%%h", "$$HOME", `back\\slash`, `\"`} {
		if !strings.Contains(def.Content, fragment) {
			t.Errorf("escaped argument missing %q:\n%s", fragment, def.Content)
		}
	}
}

func TestRenderTaskScheduler(t *testing.T) {
	spec := baseSpec()
	spec.UserID = `DOMAIN\user`
	spec.Name = "Example Agent"
	spec.Environment = nil
	spec.WorkingDirectory = `C:\state`
	spec.Executable = `C:\Program Files\agent.exe`
	def, err := Render(spec, PlatformWindows)
	if err != nil {
		t.Fatal(err)
	}
	if def.Identity != "Example Agent" || def.FileName != "Example Agent.xml" {
		t.Fatalf("identity = %q file = %q", def.Identity, def.FileName)
	}
	for _, fragment := range []string{
		`<URI>\Example Agent</URI>`,
		`<UserId>DOMAIN\user</UserId>`,
		"<RestartOnFailure><Interval>PT3S</Interval>",
		`<Command>C:\placeholder`,
	} {
		if fragment == `<Command>C:\placeholder` {
			continue
		}
		if !strings.Contains(def.Content, fragment) {
			t.Errorf("task definition missing %q:\n%s", fragment, def.Content)
		}
	}
	if !strings.Contains(def.Content, `&#34;with space&#34;`) {
		t.Errorf("arguments are not quoted for the task runner:\n%s", def.Content)
	}
	never := spec
	never.Restart = RestartNever
	if def, err = Render(never, PlatformWindows); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(def.Content, "RestartOnFailure") {
		t.Fatalf("never restart emitted RestartOnFailure:\n%s", def.Content)
	}
}

func TestRenderRejectsMalformedSpecifications(t *testing.T) {
	cases := map[string]func(*Spec){
		"emptyName":          func(s *Spec) { s.Name = "" },
		"nameTraversal":      func(s *Spec) { s.Name = "../escape" },
		"nameControl":        func(s *Spec) { s.Name = "name\nvalue" },
		"relativeExecutable": func(s *Spec) { s.Executable = "example-agent" },
		"relativeWorkdir":    func(s *Spec) { s.WorkingDirectory = "relative" },
		"nulArgument":        func(s *Spec) { s.Arguments = []string{"value\x00"} },
		"newlineArgument":    func(s *Spec) { s.Arguments = []string{"line\nbreak"} },
		"badEnvironment":     func(s *Spec) { s.Environment = map[string]string{"BAD-NAME": "value"} },
		"nulEnvironment":     func(s *Spec) { s.Environment = map[string]string{"OK": "value\x00"} },
		"badRestart":         func(s *Spec) { s.Restart = "sometimes" },
		"negativeRestart":    func(s *Spec) { s.RestartSeconds = -1 },
		"hugeRestart":        func(s *Spec) { s.RestartSeconds = 3601 },
		"badDescription":     func(s *Spec) { s.Description = "line\nbreak" },
		"tooManyArguments":   func(s *Spec) { s.Arguments = make([]string, 257) },
	}
	for name, mutate := range cases {
		spec := baseSpec()
		mutate(&spec)
		if _, err := Render(spec, PlatformDarwin); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s = %v", name, err)
		}
	}
	windows := baseSpec()
	windows.UserID = ""
	if _, err := Render(windows, PlatformWindows); !errors.Is(err, ErrInvalid) {
		t.Errorf("missing user = %v", err)
	}
	windows.UserID = "user"
	windows.WorkingDirectory = `C:\state`
	windows.Executable = `C:\agent.exe`
	windows.Environment = map[string]string{"A": "b"}
	if _, err := Render(windows, PlatformWindows); !errors.Is(err, ErrUnsupported) {
		t.Errorf("windows environment = %v", err)
	}
	windows.Environment = nil
	windows.WorkingDirectory = `C:\state`
	windows.Executable = `relative\agent.exe`
	if _, err := Render(windows, PlatformWindows); !errors.Is(err, ErrInvalid) {
		t.Errorf("windows relative executable = %v", err)
	}
	windows.Executable = `C:\Program Files\agent.exe`
	windows.WorkingDirectory = `C:\state`
	windows.Arguments = []string{`--root=C:\state`}
	if _, err := Render(windows, PlatformWindows); err != nil {
		t.Errorf("windows absolute executable = %v", err)
	}
	if _, err := Render(baseSpec(), Platform("plan9")); !errors.Is(err, ErrUnsupported) {
		t.Errorf("unsupported platform = %v", err)
	}
}

func TestPublishNeverReplacesAndReportsExisting(t *testing.T) {
	directory := t.TempDir()
	def, err := Render(baseSpec(), PlatformLinux)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Publish(def, directory)
	if err != nil || !first.Created || first.Existing {
		t.Fatalf("first publish = %#v, %v", first, err)
	}
	second, err := Publish(def, directory)
	if err != nil || second.Created || !second.Existing {
		t.Fatalf("repeat publish = %#v, %v", second, err)
	}
	changed := def
	changed.Content += "# changed\n"
	if _, err := Publish(changed, directory); !errors.Is(err, ErrExists) {
		t.Fatalf("different definition = %v", err)
	}
}

func TestPublishRejectsUnsafeLocations(t *testing.T) {
	def, err := Render(baseSpec(), PlatformLinux)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Publish(def, "relative"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("relative directory = %v", err)
	}
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err = Publish(def, link); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("symlink directory = %v", err)
	}
	open := t.TempDir()
	if err := os.Chmod(open, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(open, 0o700) })
	if _, err = Publish(def, open); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("world-writable directory = %v", err)
	}
	if _, err = Publish(Definition{Platform: PlatformLinux, FileName: "../escape", Content: "value"}, t.TempDir()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("traversal definition = %v", err)
	}
	if _, err = Publish(Definition{Platform: PlatformLinux, FileName: "x.service", Content: strings.Repeat("x", maxDefinitionBytes+1)}, t.TempDir()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized definition = %v", err)
	}
}

func TestInspectDetectsStaleNativeIdentityAndLinks(t *testing.T) {
	directory := t.TempDir()
	def, err := Render(baseSpec(), PlatformLinux)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Publish(def, directory); err != nil {
		t.Fatal(err)
	}
	inspection, err := Inspect(def, directory)
	if err != nil || !inspection.Installed || !inspection.Matches || inspection.Digest != def.Digest {
		t.Fatalf("inspection = %#v, %v", inspection, err)
	}
	if _, err = CheckDefinition(def, directory); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, def.FileName)
	if err = os.WriteFile(path, []byte("[Unit]\nDescription=other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inspection, err = Inspect(def, directory)
	if err != nil || !inspection.Installed || inspection.Matches {
		t.Fatalf("stale inspection = %#v, %v", inspection, err)
	}
	if _, err = CheckDefinition(def, directory); !errors.Is(err, ErrStale) {
		t.Fatalf("stale check = %v", err)
	}

	linkDirectory := t.TempDir()
	linkDef := def
	linkDef.FileName = "linked.service"
	if err = os.Symlink(path, filepath.Join(linkDirectory, linkDef.FileName)); err != nil {
		t.Fatal(err)
	}
	if _, err = Inspect(linkDef, linkDirectory); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("symlinked definition = %v", err)
	}
	if err = Remove(linkDef, linkDirectory); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("symlinked removal = %v", err)
	}
}

func TestRemoveDeletesOnlyTheExpectedRegularFile(t *testing.T) {
	directory := t.TempDir()
	def, err := Render(baseSpec(), PlatformDarwin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Publish(def, directory); err != nil {
		t.Fatal(err)
	}
	if err = Remove(def, directory); err != nil {
		t.Fatal(err)
	}
	if err = Remove(def, directory); err != nil {
		t.Fatalf("second removal = %v", err)
	}
	if _, err = os.Lstat(filepath.Join(directory, def.FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("definition still exists: %v", err)
	}
}

type fakeManager struct {
	platform                                   Platform
	registered, unregistered, started, stopped bool
	status                                     Status
}

func (f *fakeManager) Platform() Platform { return f.platform }
func (f *fakeManager) Name() string       { return "fake" }
func (f *fakeManager) Register(context.Context, Spec) (Registration, error) {
	f.registered = true
	return Registration{Registered: true}, nil
}
func (f *fakeManager) Unregister(context.Context, Spec) error { f.unregistered = true; return nil }
func (f *fakeManager) Start(context.Context, Spec) error      { f.started = true; return nil }
func (f *fakeManager) Stop(context.Context, Spec) error       { f.stopped = true; return nil }
func (f *fakeManager) Status(context.Context, Spec) (Status, error) {
	return f.status, nil
}

func TestServiceWrapsManagerAndReadiness(t *testing.T) {
	manager := &fakeManager{platform: PlatformLinux, status: Status{Name: "com.example.agent", Active: true}}
	ready := false
	service, err := New(manager, baseSpec(), func(context.Context) error {
		if !ready {
			return errors.New("not accepting work")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Register(context.Background()); err != nil || !manager.registered {
		t.Fatalf("register = %v", err)
	}
	if err = service.Start(context.Background()); err != nil || !manager.started {
		t.Fatalf("start = %v", err)
	}
	status, err := service.Status(context.Background())
	if err != nil || status.Ready == nil || *status.Ready {
		t.Fatalf("not-ready status = %#v, %v", status, err)
	}
	ready = true
	if status, err = service.Status(context.Background()); err != nil || status.Ready == nil || !*status.Ready {
		t.Fatalf("ready status = %#v, %v", status, err)
	}
	if err = service.Stop(context.Background()); err != nil || !manager.stopped {
		t.Fatalf("stop = %v", err)
	}
	if err = service.Unregister(context.Background()); err != nil || !manager.unregistered {
		t.Fatalf("unregister = %v", err)
	}
}

func TestServiceRequiresManagerAndValidSpec(t *testing.T) {
	if _, err := New(nil, baseSpec(), nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil manager = %v", err)
	}
	manager := &fakeManager{platform: PlatformWindows}
	if _, err := New(manager, baseSpec(), nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("windows spec without user = %v", err)
	}
	manager = &fakeManager{platform: Platform("plan9")}
	if _, err := New(manager, baseSpec(), nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported manager = %v", err)
	}
}

func TestStatusCarriesCheckedTimeOnlyFromManagers(t *testing.T) {
	now := time.Now().UTC()
	manager := &fakeManager{platform: PlatformLinux, status: Status{Name: "value", CheckedAt: now}}
	service, err := New(manager, baseSpec(), nil)
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(context.Background())
	if err != nil || status.Ready != nil || !status.CheckedAt.Equal(now) {
		t.Fatalf("status = %#v, %v", status, err)
	}
}
