package hashicorp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/webong/ext/pkg/plugin"
)

func TestPinnedProcessSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guest")
	data := []byte("reviewed bytes")
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	p := Process{Executable: path, SHA256: hex.EncodeToString(sum[:]), Protocol: "grpc"}
	if err := p.Verify(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Process){func(p *Process) { p.Protocol = "other" }, func(p *Process) { p.Executable = "relative" }, func(p *Process) { p.SHA256 = "bad" }, func(p *Process) { p.Environment = []string{"A=one", "A=two"} }, func(p *Process) { p.Arguments = []string{"bad\x00arg"} }} {
		q := p
		change(&q)
		if q.Validate() == nil {
			t.Fatalf("accepted %+v", q)
		}
	}
	if err := os.WriteFile(path, []byte("changed bytes"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := p.Verify(); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatal(err)
	}
}

func TestTypedNilEndpoint(t *testing.T) {
	var guest *plugin.Guest
	p := Plugin{Guest: guest}
	if _, err := p.Server(nil); !errors.Is(err, plugin.ErrDenied) {
		t.Fatal(err)
	}
	if err := p.GRPCServer(nil, nil); !errors.Is(err, plugin.ErrDenied) {
		t.Fatal(err)
	}
}
