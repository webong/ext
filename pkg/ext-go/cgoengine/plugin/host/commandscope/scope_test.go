package commandscope

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

func TestNativeCommandScope(t *testing.T) {
	path := os.Getenv("EXT_PLUGIN_LIBRARY")
	if path == "" {
		t.Skip("requires EXT_PLUGIN_LIBRARY")
	}
	native, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	api, err := library.BindInterface(native, "ext_plugin", 1)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := library.Describe(api)
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	executable := filepath.Join(source, "guest")
	if err := os.WriteFile(executable, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	manifest, err := packagekit.ExecutableManifest("scope-fixture", "1.0.0", []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "scope.test", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}, executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteManifest(source, manifest); err != nil {
		t.Fatal(err)
	}
	var digest string
	if err := lib.Library().Call("directory_digest", map[string]string{"root": source}, &digest); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	scope, err := Open(context.Background(), lib, Options{
		Root:   manifest.Descriptor.Identity,
		Rows:   []Row{{Manifest: manifest, Directory: source}},
		Review: func(packagekit.Manifest) (string, error) { return digest, nil },
		Dispatch: func(ctx context.Context, _ *Scope, consumer plugin.Identity, request Request) (Result, error) {
			if consumer != manifest.Descriptor.Identity {
				return Result{}, plugin.ErrDenied
			}
			if len(request.Args) == 1 && request.Args[0] == "wait" {
				close(entered)
				<-ctx.Done()
				return Result{}, ctx.Err()
			}
			if request.Settings["EXT_SCOPE_FIXTURE_SELECTOR"] != "fixture" {
				return Result{}, plugin.ErrDenied
			}
			return Result{Output: request.Input}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Close()
	pinned, err := scope.Load(manifest.Descriptor.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if pinned.Directory == source {
		t.Fatal("scope uses original package")
	}
	for _, env := range scope.Environment(manifest.Descriptor.Identity) {
		key, value, _ := strings.Cut(env, "=")
		t.Setenv(key, value)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.Setenv(SettingsEnv, `{"EXT_SCOPE_FIXTURE_SELECTOR":"fixture"}`)
	var out, stderr bytes.Buffer
	if code, err := Relay(ctx, []string{"echo"}, strings.NewReader("scoped-value"), &out, &stderr); err != nil || code != 0 || out.String() != "scoped-value" {
		t.Fatalf("relay: %d %v %q %q", code, err, out.String(), stderr.String())
	}
	token := os.Getenv(TokenEnv)
	t.Setenv(TokenEnv, strings.Repeat("0", 64))
	out.Reset()
	stderr.Reset()
	if code, err := Relay(ctx, nil, strings.NewReader(""), &out, &stderr); err != nil || code == 0 || out.Len() != 0 {
		t.Fatalf("forged token: %d %v %q", code, err, out.String())
	}
	t.Setenv(TokenEnv, token)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var output, failure bytes.Buffer
		_, _ = Relay(ctx, []string{"wait"}, strings.NewReader(""), &output, &failure)
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("dispatch did not start")
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("relay survived scope close")
	}
	if _, err := os.Stat(pinned.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("snapshot survived close: %v", err)
	}
}
