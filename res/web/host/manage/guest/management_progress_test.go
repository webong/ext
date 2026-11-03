package guest

import (
	"bytes"
	"testing"

	"github.com/webong/ext/res/web/host/artifact/extension"
	"github.com/webong/ext/res/web/host/contract"
)

// Hosts parse adapter progress with extension.ParseProgress, so the adapter side
// must emit exactly that form, one line per report.
func TestWriteManagementProgressIsParsable(t *testing.T) {
	var out bytes.Buffer
	want := extension.InstallResult{Status: contract.StatusActivated, Browser: "example", ID: "abc"}
	if err := writeManagementProgress(&out, want); err != nil {
		t.Fatal(err)
	}
	if bytes.Count(out.Bytes(), []byte("\n")) != 1 {
		t.Fatalf("expected one line: %q", out.String())
	}
	got, ok := extension.ParseProgress(out.String())
	if !ok || got.Status != want.Status || got.ID != want.ID {
		t.Fatalf("%+v %v", got, ok)
	}
	if err := writeManagementProgress(nil, want); err != nil {
		t.Fatal(err)
	}
}
