package systemd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webong/ext/res/system/service"
)

type call struct {
	executable string
	arguments  []string
}

type fakeRunner struct {
	fields    []call
	show      string
	showError error
	failed    map[string]error
}

func (f *fakeRunner) run(_ context.Context, _ string, executable string, arguments ...string) ([]byte, error) {
	f.fields = append(f.fields, call{executable: executable, arguments: append([]string(nil), arguments...)})
	key := executable + " " + strings.Join(arguments, " ")
	if err, ok := f.failed[key]; ok {
		return nil, err
	}
	if len(arguments) >= 3 && arguments[1] == "show" {
		return []byte(f.show), f.showError
	}
	return nil, nil
}

func (f *fakeRunner) invoked(fragment string) bool {
	for _, field := range f.fields {
		value := field.executable + " " + strings.Join(field.arguments, " ")
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func testManager(t *testing.T, run *fakeRunner) (*Manager, string) {
	t.Helper()
	directory := t.TempDir()
	manager, err := New(Config{Directory: directory, Executable: "systemctl", Run: run.run})
	if err != nil {
		t.Fatal(err)
	}
	return manager, directory
}

func testSpec() service.Spec {
	return service.Spec{
		Name: "example-agent", Description: "Example agent",
		Executable:       "/usr/local/bin/example-agent",
		Arguments:        []string{"--serve"},
		Environment:      map[string]string{"EXAMPLE_ROOT": "/var/lib/example"},
		WorkingDirectory: "/var/lib/example",
		Restart:          service.RestartOnFailure,
	}
}

func TestRegisterPublishesAndEnables(t *testing.T) {
	runner := &fakeRunner{show: "LoadState=not-found\nFragmentPath=\n"}
	manager, directory := testManager(t, runner)
	registration, err := manager.Register(context.Background(), testSpec())
	if err != nil {
		t.Fatal(err)
	}
	if !registration.Registered || !registration.Publication.Created {
		t.Fatalf("registration = %#v", registration)
	}
	if registration.Definition.Identity != "example-agent.service" {
		t.Fatalf("unit identity = %q", registration.Definition.Identity)
	}
	if _, err = os.Stat(filepath.Join(directory, "example-agent.service")); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"--user daemon-reload", "--user enable --now example-agent.service"} {
		if !runner.invoked(fragment) {
			t.Errorf("missing command %q: %#v", fragment, runner.fields)
		}
	}
	runner.show = "LoadState=loaded\nFragmentPath=" + filepath.Join(directory, "example-agent.service") + "\n"
	repeated, err := manager.Register(context.Background(), testSpec())
	if !errors.Is(err, service.ErrRegistered) || repeated.Registered {
		t.Fatalf("repeated registration = %#v, %v", repeated, err)
	}
}

func TestStatusTracksNativeState(t *testing.T) {
	runner := &fakeRunner{show: "LoadState=not-found\nFragmentPath=\n"}
	manager, directory := testManager(t, runner)
	if _, err := manager.Register(context.Background(), testSpec()); err != nil {
		t.Fatal(err)
	}
	runner.show = "LoadState=loaded\nActiveState=active\nFragmentPath=" + filepath.Join(directory, "example-agent.service") + "\n"
	status, err := manager.Status(context.Background(), testSpec())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || !status.Registered || !status.Active || status.Stale {
		t.Fatalf("status = %#v", status)
	}
	if status.Detail != "load=loaded active=active" {
		t.Fatalf("detail = %q", status.Detail)
	}
	runner.show = "LoadState=loaded\nActiveState=inactive\n"
	if status, err = manager.Status(context.Background(), testSpec()); err != nil || status.Active || !status.Registered {
		t.Fatalf("inactive status = %#v, %v", status, err)
	}
	runner.showError = errors.New("dbus unavailable")
	if status, err = manager.Status(context.Background(), testSpec()); err != nil || status.Registered {
		t.Fatalf("unavailable manager status = %#v, %v", status, err)
	}
}

func TestStaleDefinitionIsRefused(t *testing.T) {
	runner := &fakeRunner{show: "LoadState=not-found\n"}
	manager, directory := testManager(t, runner)
	if _, err := manager.Register(context.Background(), testSpec()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "example-agent.service"), []byte("[Unit]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background(), testSpec()); !errors.Is(err, service.ErrStale) {
		t.Fatalf("stale start = %v", err)
	}
	if err := manager.Stop(context.Background(), testSpec()); !errors.Is(err, service.ErrStale) {
		t.Fatalf("stale stop = %v", err)
	}
	if err := manager.Unregister(context.Background(), testSpec()); !errors.Is(err, service.ErrStale) {
		t.Fatalf("stale unregister = %v", err)
	}
}

func TestLifecycleCommandsAndRemoval(t *testing.T) {
	runner := &fakeRunner{show: "LoadState=not-found\n"}
	manager, directory := testManager(t, runner)
	if err := manager.Start(context.Background(), testSpec()); !errors.Is(err, service.ErrNotInstalled) {
		t.Fatalf("start without definition = %v", err)
	}
	if _, err := manager.Register(context.Background(), testSpec()); err != nil {
		t.Fatal(err)
	}
	runner.show = "LoadState=loaded\nActiveState=active\n"
	if err := manager.Start(context.Background(), testSpec()); err != nil {
		t.Fatal(err)
	}
	if !runner.invoked("--user start example-agent.service") {
		t.Fatalf("start command missing: %#v", runner.fields)
	}
	if err := manager.Stop(context.Background(), testSpec()); err != nil {
		t.Fatal(err)
	}
	if !runner.invoked("--user stop example-agent.service") {
		t.Fatalf("stop command missing: %#v", runner.fields)
	}
	if err := manager.Unregister(context.Background(), testSpec()); err != nil {
		t.Fatal(err)
	}
	if !runner.invoked("--user disable --now example-agent.service") {
		t.Fatalf("disable command missing: %#v", runner.fields)
	}
	if _, err := os.Stat(filepath.Join(directory, "example-agent.service")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("definition still exists: %v", err)
	}
}

func TestNewRequiresAbsoluteDirectory(t *testing.T) {
	if _, err := New(Config{Directory: "relative"}); !errors.Is(err, service.ErrInvalid) {
		t.Fatalf("relative directory = %v", err)
	}
	if _, err := New(Config{Directory: t.TempDir(), Executable: "/bin/false"}); err != nil {
		t.Fatalf("explicit executable = %v", err)
	}
}

func TestParsePropertiesIgnoresMalformedLines(t *testing.T) {
	properties := parseProperties([]byte("LoadState=loaded\nnot-a-property\nActiveState=active\n=empty\n"))
	if properties["LoadState"] != "loaded" || properties["ActiveState"] != "active" || len(properties) != 2 {
		t.Fatalf("properties = %#v", properties)
	}
}
