package hashicorp

import (
	"errors"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
)

// Serve's success path owns the process (go-plugin serves until the host
// disconnects), so the real launch is covered by the guest in testdata, which
// serves through it. Its refusals return at once and are checked here.
func TestServeRefusesBeforeServing(t *testing.T) {
	var typedNil *plugin.Guest
	if err := Serve(nil, "grpc"); !errors.Is(err, plugin.ErrDenied) {
		t.Fatalf("nil endpoint: %v", err)
	}
	if err := Serve(typedNil, "grpc"); !errors.Is(err, plugin.ErrDenied) {
		t.Fatalf("typed nil endpoint: %v", err)
	}
	for _, protocol := range []string{"", "tcp", "GRPC"} {
		if err := Serve(&plugin.Guest{}, protocol); !errors.Is(err, plugin.ErrUnsupported) {
			t.Fatalf("protocol %q: %v", protocol, err)
		}
	}
}
