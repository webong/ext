package resourcecmd

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestCanceledRequestDoesNotOpenStore(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, operation := range []string{"list", "inspect", "call"} {
		var out, stderr bytes.Buffer
		code := (Manager{Home: home}).RunContext(ctx, []string{operation, "example/provider", `{}`}, &out, &stderr)
		if code != 1 || out.Len() != 0 || !strings.Contains(stderr.String(), "context canceled") {
			t.Fatalf("%s: code=%d out=%s stderr=%s", operation, code, out.String(), stderr.String())
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled request touched store: %v %v", entries, err)
	}
}
