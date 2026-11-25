package plugin_test

import (
	"errors"
	"reflect"
	"testing"

	shared "github.com/webong/ext/pkg/ext-go/core/plugin"
	routes "github.com/webong/ext/pkg/ext-go/core/plugin/interop"
	legacy "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/interop"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/jsonline"
)

// Assignments protect source compatibility for callers using the published paths.
var (
	_ func([]legacy.BackendProfile, []legacy.BackendProfile, []interop.Bridge, interop.Requirements) (interop.Route, error) = routes.Resolve
	_ func([]shared.BackendProfile, []shared.BackendProfile, []routes.Bridge, routes.Requirements) (routes.Route, error)    = interop.Resolve
	_ func() legacy.BackendProfile                                                                                          = shared.JSONLineProfile
)

func TestSharedBackendContractIdentity(t *testing.T) {
	for _, pair := range [][2]reflect.Type{
		{reflect.TypeFor[legacy.Backend](), reflect.TypeFor[shared.Backend]()},
		{reflect.TypeFor[legacy.Endpoint](), reflect.TypeFor[shared.Endpoint]()},
		{reflect.TypeFor[legacy.Descriptor](), reflect.TypeFor[shared.Descriptor]()},
		{reflect.TypeFor[legacy.Request](), reflect.TypeFor[shared.Request]()},
		{reflect.TypeFor[legacy.Response](), reflect.TypeFor[shared.Response]()},
		{reflect.TypeFor[legacy.RemoteError](), reflect.TypeFor[shared.RemoteError]()},
		{reflect.TypeFor[interop.Bridge](), reflect.TypeFor[routes.Bridge]()},
		{reflect.TypeFor[interop.Route](), reflect.TypeFor[routes.Route]()},
		{reflect.TypeFor[interop.Requirements](), reflect.TypeFor[routes.Requirements]()},
	} {
		if pair[0] != pair[1] {
			t.Fatalf("type identity differs: %v / %v", pair[0], pair[1])
		}
	}
	for _, pair := range [][2]error{
		{legacy.ErrInvalid, shared.ErrInvalid}, {legacy.ErrUnsupported, shared.ErrUnsupported},
		{legacy.ErrNotFound, shared.ErrNotFound}, {legacy.ErrAmbiguous, shared.ErrAmbiguous},
		{legacy.ErrDenied, shared.ErrDenied}, {legacy.ErrMismatch, shared.ErrMismatch},
		{legacy.ErrClosed, shared.ErrClosed}, {legacy.ErrDraining, shared.ErrDraining},
	} {
		if pair[0] != pair[1] {
			t.Fatalf("error identity differs: %v / %v", pair[0], pair[1])
		}
	}
	if legacy.DefaultTimeout != shared.DefaultTimeout || legacy.MaxFrameBytes != shared.MaxFrameBytes {
		t.Fatal("wire defaults changed")
	}
}

func TestSharedRoutesPreservePublishedFacade(t *testing.T) {
	p := jsonline.Profile()
	if !reflect.DeepEqual(p, shared.JSONLineProfile()) {
		t.Fatal("JSON-line profile changed")
	}
	for _, tc := range []struct {
		name         string
		hosts        []shared.BackendProfile
		requirements routes.Requirements
		want         error
	}{
		{"direct", []shared.BackendProfile{p}, routes.Requirements{}, nil},
		{"empty", nil, routes.Requirements{}, shared.ErrInvalid},
		{"streaming", []shared.BackendProfile{p}, routes.Requirements{NativeStreaming: true}, shared.ErrUnsupported},
		{"boundary", []shared.BackendProfile{p}, routes.Requirements{Boundaries: []string{shared.BoundaryKernel}}, shared.ErrDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := interop.Resolve(tc.hosts, []shared.BackendProfile{p}, nil, tc.requirements)
			want, nativeErr := routes.Resolve(tc.hosts, []shared.BackendProfile{p}, nil, tc.requirements)
			if !reflect.DeepEqual(got, want) || !errors.Is(err, tc.want) || !errors.Is(nativeErr, tc.want) {
				t.Fatalf("legacy=%+v, %v; core=%+v, %v", got, err, want, nativeErr)
			}
		})
	}
	// Returned capability slices remain caller-owned.
	p.Protocols[0] = "changed"
	if shared.JSONLineProfile().Protocols[0] != shared.APIVersion {
		t.Fatal("profile shares mutable state")
	}
}
