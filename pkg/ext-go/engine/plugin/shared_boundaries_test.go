package plugin_test

import (
	"reflect"
	"testing"

	shared "github.com/webong/ext/pkg/ext-go/core/plugin"
	legacy "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/jsonline"
)

func TestSharedBackendContractIdentity(t *testing.T) {
	for _, pair := range [][2]reflect.Type{
		{reflect.TypeFor[legacy.Backend](), reflect.TypeFor[shared.Backend]()},
		{reflect.TypeFor[legacy.Endpoint](), reflect.TypeFor[shared.Endpoint]()},
		{reflect.TypeFor[legacy.Descriptor](), reflect.TypeFor[shared.Descriptor]()},
		{reflect.TypeFor[legacy.Request](), reflect.TypeFor[shared.Request]()},
		{reflect.TypeFor[legacy.Response](), reflect.TypeFor[shared.Response]()},
		{reflect.TypeFor[legacy.RemoteError](), reflect.TypeFor[shared.RemoteError]()},
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

func TestJSONLineProfile(t *testing.T) {
	p := jsonline.Profile()
	if !reflect.DeepEqual(p, shared.JSONLineProfile()) {
		t.Fatal("JSON-line profile changed")
	}
	p.Protocols[0] = "changed"
	if shared.JSONLineProfile().Protocols[0] != shared.APIVersion {
		t.Fatal("profile shares mutable state")
	}
}
