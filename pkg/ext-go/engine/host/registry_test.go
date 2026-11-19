package host_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host"
	"github.com/webong/ext/pkg/ext-go/engine/host/process"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/package"
	"github.com/webong/ext/pkg/ext-go/engine/plugintest"
	"github.com/webong/ext/pkg/ext-go/engine/transport/jsonline"
)

const relayEnv, relayCasesEnv = "HOST_TEST_RELAY", "HOST_TEST_RELAY_CASES"

// serveRelay serves the registry fixture's relay guest when this test binary is
// started as one (see TestMain).
func serveRelay(name string) int {
	cases, err := plugintest.ReadRegistryCases(os.Getenv(relayCasesEnv))
	if err != nil {
		return 1
	}
	guest, err := cases.RelayGuest(name)
	if err != nil {
		return 1
	}
	if jsonline.ServeStdio(context.Background(), guest) != nil {
		return 1
	}
	return 0
}

func registryCases(t *testing.T) (string, plugintest.RegistryCases) {
	t.Helper()
	path, err := plugintest.RegistryCasesPath()
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("registry-cases.json is in the repository only")
	}
	if err != nil {
		t.Fatal(err)
	}
	cases, err := plugintest.ReadRegistryCases(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, cases
}

// installRelay installs a package whose executable is this test binary and
// whose descriptor is the named relay guest's.
func installRelay(t *testing.T, cases plugintest.RegistryCases, name string) store.Installed {
	t.Helper()
	body, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	exe := "relay"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if err := os.WriteFile(filepath.Join(src, exe), body, 0o700); err != nil {
		t.Fatal(err)
	}
	d := cases.RelayDescriptor(name)
	m, err := packagekit.ExecutableManifest(d.Identity.ID, d.Identity.Version, d.Contracts, filepath.Join(src, exe))
	if err != nil {
		t.Fatal(err)
	}
	m.Descriptor = d
	if err := store.WriteManifest(src, m); err != nil {
		t.Fatal(err)
	}
	s, err := store.New(filepath.Join(t.TempDir(), "plugins"), store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := s.Install(src)
	if err != nil {
		t.Fatal(err)
	}
	return installed
}

func allowOrDeny(deny bool) func(context.Context, plugin.Request) error {
	return func(context.Context, plugin.Request) error {
		if deny {
			return plugin.ErrDenied
		}
		return nil
	}
}

type registryHost struct {
	t         *testing.T
	path      string
	installed map[string]store.Installed
	registry  *host.Registry
	procs     map[string]*host.Process
	all       []*host.Process
}

func (h *registryHost) open(name string, denyForward, denyReverse bool) (*host.Process, error) {
	installed := h.installed[name]
	p, err := host.Open(context.Background(), installed, host.Options{
		Launch:    &process.Command{Path: mustExecutable(h.t, installed), Env: []string{relayEnv + "=" + name, relayCasesEnv + "=" + h.path}},
		Authorize: allowOrDeny(denyForward),
		Reverse:   &host.ReverseOptions{Authorize: allowOrDeny(denyReverse)},
		Registry:  h.registry,
	})
	if err == nil {
		h.all = append(h.all, p)
	}
	return p, err
}
func (h *registryHost) Open(name string, contract plugin.ContractRef, denyForward, denyReverse bool) error {
	p, err := h.open(name, denyForward, denyReverse)
	if err != nil {
		return err
	}
	h.procs[name] = p
	return h.registry.Register(contract, p)
}
func (h *registryHost) Route(e plugintest.RegistryEdge) error {
	return h.registry.Route(e.From.Ref(), e.From.Operation, e.To.Ref(), e.To.Operation)
}
func (h *registryHost) Unregister(contract plugin.ContractRef) error {
	return h.registry.Unregister(contract)
}
func (h *registryHost) Call(ctx context.Context, name string, contract plugin.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	err := h.procs[name].Call(ctx, contract.Name+"."+operation, payload, &out)
	return out, err
}
func (h *registryHost) Close() {
	for _, p := range h.all {
		_ = p.Close()
	}
}

func newRegistryHost(t *testing.T) (*registryHost, plugintest.RegistryCases) {
	path, cases := registryCases(t)
	h := &registryHost{t: t, path: path, installed: map[string]store.Installed{}}
	for name := range cases.Guests {
		h.installed[name] = installRelay(t, cases, name)
	}
	return h, cases
}

func TestRegistryCases(t *testing.T) {
	base, cases := newRegistryHost(t)
	plugintest.RunRegistryCases(t, cases, func(t *testing.T) plugintest.RegistryHost {
		return &registryHost{t: t, path: base.path, installed: base.installed, registry: host.NewRegistry(), procs: map[string]*host.Process{}}
	})
}

func TestRegistryConfiguration(t *testing.T) {
	h, cases := newRegistryHost(t)
	h.registry, h.procs = host.NewRegistry(), map[string]*host.Process{}
	defer h.Close()
	b, err := h.open("b", false, false)
	if err != nil {
		t.Fatal(err)
	}
	ref := cases.Contract("b")
	for _, c := range []struct {
		ref  plugin.ContractRef
		p    *host.Process
		want error
	}{
		{ref, nil, plugin.ErrInvalid},
		{plugin.ContractRef{Name: "Not A Name", Version: "v1"}, b, plugin.ErrInvalid},
		{plugin.ContractRef{Name: ref.Name, Version: "v 1"}, b, plugin.ErrInvalid},
		{plugin.ContractRef{Name: "registry.other", Version: "v1"}, b, plugin.ErrUnsupported},
		{plugin.ContractRef{Name: ref.Name, Version: "v2"}, b, plugin.ErrUnsupported},
		{ref, b, nil},
		{ref, b, host.ErrExists},
	} {
		if err := h.registry.Register(c.ref, c.p); !errors.Is(err, c.want) {
			t.Errorf("Register(%v) = %v, want %v", c.ref, err, c.want)
		}
	}
	ask := plugin.ContractRef{Name: "registry.ask", Version: "v1"}
	if err := h.registry.Route(ask, "echo-b", ref, "echo"); err != nil {
		t.Fatal(err)
	}
	if err := h.registry.Route(ask, "echo-b", ref, "echo"); !errors.Is(err, host.ErrExists) {
		t.Errorf("second Route = %v", err)
	}
	if err := h.registry.Route(ask, "other", ref, "Bad Name"); !errors.Is(err, plugin.ErrInvalid) {
		t.Errorf("invalid Route = %v", err)
	}
	if err := h.registry.Unregister(plugin.ContractRef{Name: "registry.x", Version: "v1"}); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("Unregister unknown = %v", err)
	}
	if err := h.registry.Unregister(ref); err != nil {
		t.Fatal(err)
	}
	// A source needs reverse options with Authorize and without its own Handle.
	installed := h.installed["a"]
	launch := &process.Command{Path: mustExecutable(t, installed), Env: []string{relayEnv + "=a", relayCasesEnv + "=" + h.path}}
	if _, err := host.Open(context.Background(), installed, host.Options{Launch: launch, Registry: h.registry}); !errors.Is(err, plugin.ErrDenied) {
		t.Errorf("Open without Reverse = %v", err)
	}
	own := &host.ReverseOptions{Authorize: allowOrDeny(false), Handle: func(context.Context, plugin.Request) (json.RawMessage, error) { return nil, nil }}
	if _, err := host.Open(context.Background(), installed, host.Options{Launch: launch, Reverse: own, Registry: h.registry}); !errors.Is(err, plugin.ErrInvalid) {
		t.Errorf("Open with a Handle = %v", err)
	}
}

// Sources call through the registry while the target is unregistered and
// registered under them: every call is answered or refused, and Unregister
// waits for the calls in flight.
func TestRegistryConcurrentRegistration(t *testing.T) {
	h, cases := newRegistryHost(t)
	h.registry, h.procs = host.NewRegistry(), map[string]*host.Process{}
	defer h.Close()
	b, err := h.open("b", false, false)
	if err != nil {
		t.Fatal(err)
	}
	ref := cases.Contract("b")
	ask := plugin.ContractRef{Name: "registry.ask", Version: "v1"}
	if err := h.registry.Route(ask, "echo-b", ref, "echo"); err != nil {
		t.Fatal(err)
	}
	if err := h.registry.Register(ref, b); err != nil {
		t.Fatal(err)
	}
	var sources []*host.Process
	for range 4 {
		p, err := h.open("a", false, false)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, p)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	answered, refused := 0, 0
	for _, p := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 25 {
				var out struct {
					Payload *int                `json:"payload"`
					Error   *plugin.RemoteError `json:"error"`
				}
				payload, _ := json.Marshal(map[string]any{"operation": "echo-b", "payload": i})
				if err := p.Call(context.Background(), "registry.a.relay", json.RawMessage(payload), &out); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				switch {
				case out.Payload != nil && *out.Payload == i:
					answered++
				case out.Error != nil && out.Error.Code == plugin.ReverseUnavailable:
					refused++
				default:
					t.Errorf("relay %d: %+v", i, out)
				}
				mu.Unlock()
			}
		}()
	}
	for i := range 20 {
		if err := h.registry.Route(ask, "extra-"+string(rune('a'+i)), ref, "echo"); err != nil {
			t.Fatal(err)
		}
		if err := h.registry.Unregister(ref); err != nil {
			t.Fatal(err)
		}
		if err := h.registry.Register(ref, b); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	if answered+refused != 100 || answered == 0 {
		t.Fatalf("answered %d, refused %d", answered, refused)
	}
}
