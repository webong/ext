package hashicorp_test

import (
	"errors"
	"github.com/webong/ext/pkg/plugin"
	hashicorp "github.com/webong/ext/pkg/plugin-hashicorp"
	"github.com/webong/ext/pkg/plugin/interop"
	"github.com/webong/ext/pkg/plugin/jsonline"
	"testing"
)

func TestRoutes(t *testing.T) {
	line, grpc := jsonline.Profile(), hashicorp.GRPCProfile()
	b, err := hashicorp.JSONLineBridge("grpc")
	if err != nil {
		t.Fatal(err)
	}
	r, err := interop.Resolve([]plugin.BackendProfile{line}, []plugin.BackendProfile{grpc}, []interop.Bridge{b}, interop.Requirements{})
	if err != nil || r.Bridge != b.Name || r.Concurrent || r.NativeCallbacks || r.Protocol != plugin.APIVersion {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err = interop.Resolve([]plugin.BackendProfile{line}, []plugin.BackendProfile{grpc}, nil, interop.Requirements{}); !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err = interop.Resolve([]plugin.BackendProfile{line}, []plugin.BackendProfile{grpc}, []interop.Bridge{b}, interop.Requirements{Concurrent: true}); !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatal(err)
	}
	r, err = interop.Resolve([]plugin.BackendProfile{line}, []plugin.BackendProfile{grpc, line}, []interop.Bridge{b}, interop.Requirements{})
	if err != nil || r.Bridge != "" {
		t.Fatalf("direct preference: %+v %v", r, err)
	}
	line.Protocols[0] = "ext.plugin/v2"
	if r.Host.Protocols[0] != plugin.APIVersion {
		t.Fatal("route aliased inputs")
	}
	_, err = interop.Resolve([]plugin.BackendProfile{line}, []plugin.BackendProfile{grpc}, []interop.Bridge{b}, interop.Requirements{})
	if !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatal(err)
	}
	line = jsonline.Profile()
	line.Protocols = append(line.Protocols, line.Protocols[0])
	if _, err = interop.Resolve([]plugin.BackendProfile{line}, []plugin.BackendProfile{grpc}, nil, interop.Requirements{}); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatal(err)
	}
}
