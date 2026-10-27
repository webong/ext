//go:build darwin

package launchd

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
	fields     []call
	output     map[string]string
	failed     map[string]error
	registered map[string]bool
	print      bool
}

func (f *fakeRunner) run(_ context.Context, _ string, executable string, arguments ...string) ([]byte, error) {
	f.fields = append(f.fields, call{executable: executable, arguments: append([]string(nil), arguments...)})
	key := executable + " " + strings.Join(arguments, " ")
	if err, ok := f.failed[key]; ok {
		return []byte(f.output[key]), err
	}
	if len(arguments) > 0 {
		last := arguments[len(arguments)-1]
		label := strings.TrimSuffix(last[strings.LastIndex(last, "/")+1:], ".plist")
		switch arguments[0] {
		case "bootstrap":
			if f.registered == nil {
				f.registered = map[string]bool{}
			}
			f.registered[label] = true
			return nil, nil
		case "bootout":
			delete(f.registered, label)
			return nil, nil
		case "print":
			if f.print || f.registered[label] {
				return []byte(f.output[key]), nil
			}
			return []byte("Could not find service"), errors.New("exit status 113")
		}
	}
	return []byte(f.output[key]), nil
}

func (f *fakeRunner) contains(executable, argument string) bool {
	for _, field := range f.fields {
		if field.executable == executable && strings.Contains(strings.Join(field.arguments, " "), argument) {
			return true
		}
	}
	return false
}

func testManager(t *testing.T, run *fakeRunner) (*Manager, string) {
	t.Helper()
	directory := t.TempDir()
	manager, err := New(Config{Directory: directory, UID: os.Getuid(), Run: run.run})
	if err != nil {
		t.Fatal(err)
	}
	return manager, directory
}

func testSpec() service.Spec {
	return service.Spec{
		Name: "com.example.agent", Description: "Example agent",
		Executable:       "/usr/local/bin/example-agent",
		Arguments:        []string{"--serve"},
		Environment:      map[string]string{"EXAMPLE_ROOT": "/var/lib/example"},
		WorkingDirectory: "/var/lib/example", Restart: service.RestartOnFailure,
	}
}

func TestRegisterPublishesAndBootstraps(t *testing.T) {
	runner := &fakeRunner{}
	manager, directory := testManager(t, runner)
	registration, err := manager.Register(context.Background(), testSpec())
	if err != nil {
		t.Fatal(err)
	}
	if !registration.Registered || !registration.Publication.Created {
		t.Fatalf("registration = %#v", registration)
	}
	if _, err := os.Stat(filepath.Join(directory, "com.example.agent.plist")); err != nil {
		t.Fatal(err)
	}
	if !runner.contains("/bin/launchctl", "bootstrap") {
		t.Fatalf("bootstrap was not invoked: %#v", runner.fields)
	}

	repeated, err := manager.Register(context.Background(), testSpec())
	if !errors.Is(err, service.ErrRegistered) || repeated.Registered {
		t.Fatalf("repeated registration = %#v, %v", repeated, err)
	}
	if len(runner.fields) != 3 {
		t.Fatalf("repeated registration ran unexpected commands: %#v", runner.fields)
	}
}

func TestStatusAndLifecycle(t *testing.T) {
	runner := &fakeRunner{output: map[string]string{}}
	manager, directory := testManager(t, runner)
	if _, err := manager.Register(context.Background(), testSpec()); err != nil {
		t.Fatal(err)
	}
	runner.print = true
	runner.output["/bin/launchctl print gui/"+itoa(os.Getuid())+"/com.example.agent"] = "state = running\npid = 42\n"
	status, err := manager.Status(context.Background(), testSpec())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || !status.Registered || !status.Active || status.Stale || status.Manager != "launchd" {
		t.Fatalf("status = %#v", status)
	}
	if status.DefinitionDigest == "" || status.DefinitionPath != filepath.Join(directory, "com.example.agent.plist") {
		t.Fatalf("definition identity = %#v", status)
	}
	if err = manager.Start(context.Background(), testSpec()); err != nil {
		t.Fatal(err)
	}
	if !runner.contains("/bin/launchctl", "kickstart") {
		t.Fatalf("start was not invoked: %#v", runner.fields)
	}
	if err = manager.Stop(context.Background(), testSpec()); err != nil {
		t.Fatal(err)
	}
	if !runner.contains("/bin/launchctl", "kill SIGTERM") {
		t.Fatalf("stop was not invoked: %#v", runner.fields)
	}
	if err = manager.Unregister(context.Background(), testSpec()); err != nil {
		t.Fatal(err)
	}
	if !runner.contains("/bin/launchctl", "bootout") {
		t.Fatalf("bootout was not invoked: %#v", runner.fields)
	}
	if _, err = os.Stat(filepath.Join(directory, "com.example.agent.plist")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("definition still exists: %v", err)
	}
}

func TestStaleDefinitionIsRefused(t *testing.T) {
	runner := &fakeRunner{}
	manager, directory := testManager(t, runner)
	if _, err := manager.Register(context.Background(), testSpec()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "com.example.agent.plist")
	if err := os.WriteFile(path, []byte("replaced\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(context.Background(), testSpec())
	if err != nil || !status.Stale {
		t.Fatalf("stale status = %#v, %v", status, err)
	}
	if err = manager.Start(context.Background(), testSpec()); !errors.Is(err, service.ErrStale) {
		t.Fatalf("stale start = %v", err)
	}
	if err = manager.Stop(context.Background(), testSpec()); !errors.Is(err, service.ErrStale) {
		t.Fatalf("stale stop = %v", err)
	}
	if err = manager.Unregister(context.Background(), testSpec()); !errors.Is(err, service.ErrStale) {
		t.Fatalf("stale unregister = %v", err)
	}
}

func TestLifecycleRequiresPublishedDefinition(t *testing.T) {
	runner := &fakeRunner{}
	manager, _ := testManager(t, runner)
	if err := manager.Start(context.Background(), testSpec()); !errors.Is(err, service.ErrNotInstalled) {
		t.Fatalf("start without definition = %v", err)
	}
	if err := manager.Stop(context.Background(), testSpec()); !errors.Is(err, service.ErrNotInstalled) {
		t.Fatalf("stop without definition = %v", err)
	}
	status, err := manager.Status(context.Background(), testSpec())
	if err != nil || status.Installed || status.Registered {
		t.Fatalf("status without definition = %#v, %v", status, err)
	}
}

func TestUnregisterKeepsDefinitionWhenManagerRejects(t *testing.T) {
	runner := &fakeRunner{}
	manager, directory := testManager(t, runner)
	if _, err := manager.Register(context.Background(), testSpec()); err != nil {
		t.Fatal(err)
	}
	runner.print = true
	runner.failed = map[string]error{}
	bootout := "/bin/launchctl bootout gui/" + itoa(os.Getuid()) + "/com.example.agent"
	runner.failed[bootout] = errors.New("permission denied")
	if err := manager.Unregister(context.Background(), testSpec()); err == nil {
		t.Fatal("unregister ignored manager rejection")
	}
	if _, err := os.Stat(filepath.Join(directory, "com.example.agent.plist")); err != nil {
		t.Fatalf("definition was removed after a refused unregister: %v", err)
	}
}

func TestNewRejectsUnsafeConfiguration(t *testing.T) {
	if _, err := New(Config{Directory: "relative", UID: 501}); !errors.Is(err, service.ErrInvalid) {
		t.Fatalf("relative directory = %v", err)
	}
	if _, err := New(Config{Directory: t.TempDir(), UID: 0}); !errors.Is(err, service.ErrInvalid) {
		t.Fatalf("root user = %v", err)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}
