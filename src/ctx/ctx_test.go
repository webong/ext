package ctx_test

import (
	"bytes"
	"testing"

	"github.com/webong/ext/ctx"
)

func TestRunIsTheCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := ctx.Run([]string{"--version"}, &out, &errOut); code != 0 {
		t.Fatalf("exit code %d, stderr %q", code, errOut.String())
	}
	if out.Len() == 0 {
		t.Fatal("--version wrote nothing to stdout")
	}
}
