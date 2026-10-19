//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

// The C engine does not carry reverse calls or progress frames yet. Until it
// does, it refuses them at open rather than running without them.

func TestCEngineRefusesHostServices(t *testing.T) {
	d := testDescriptor()
	options := referenceOptions(&testBackend{d: d})
	options.Reverse = &plugin.ReverseOptions{
		Handle:    func(context.Context, plugin.Request) (json.RawMessage, error) { return nil, nil },
		Authorize: func(context.Context, plugin.Request) error { return nil },
	}
	if _, err := Open(context.Background(), d, options); !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatalf("Reverse on the C engine: %v", err)
	}
}

func TestCEngineRefusesDescriptorsThatUseReverseOrProgress(t *testing.T) {
	withReverse := testDescriptor()
	withReverse.Reverse = []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "host.svc", Version: "v1"}, Operations: []plugin.Operation{{Name: "sign"}}}}
	withProgress := testDescriptor()
	withProgress.Contracts[0].Operations[0].Progress = true
	for name, d := range map[string]plugin.Descriptor{"reverse": withReverse, "progress": withProgress} {
		t.Run(name, func(t *testing.T) {
			if s, err := Open(context.Background(), d, referenceOptions(&testBackend{d: d})); err == nil {
				_ = s.Abort()
				t.Fatal("the C engine opened a plugin whose descriptor it cannot honor")
			}
		})
	}
}

func TestCEngineSessionDoneClosesWithTheSession(t *testing.T) {
	d := testDescriptor()
	s, err := Open(context.Background(), d, referenceOptions(&testBackend{d: d}))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
		t.Fatal("Done closed before the session ended")
	default:
	}
	if err := s.Abort(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	default:
		t.Fatal("Done did not close with the session")
	}
}
