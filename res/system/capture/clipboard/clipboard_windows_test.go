//go:build windows

package clipboard

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// nativeClipboard is the Win32 clipboard under a read/write policy, or a skip when this session
// has none (OpenClipboard keeps failing, as in a service session). The tests replace the
// clipboard's content and restore nothing.
func nativeClipboard(t *testing.T, formats ...string) *Clipboard {
	t.Helper()
	ctx := context.Background()
	if err := openClipboard(ctx); err != nil {
		if strings.Contains(err.Error(), "held by another process") {
			t.Skipf("no clipboard in this session: %v", err)
		}
		t.Fatal(err)
	}
	procCloseClipboard.Call()
	clipboard, err := New(WindowsBackend(), Policy{MaxBytes: 1 << 20, AllowedFormats: formats, AllowRead: true, AllowWrite: true})
	if err != nil {
		t.Fatal(err)
	}
	return clipboard
}

func text(value string) []Item {
	return []Item{{Representations: []Representation{{Format: "CF_UNICODETEXT", Data: []byte(value)}}}}
}

func readText(t *testing.T, clipboard *Clipboard) (string, string) {
	t.Helper()
	snapshot, err := clipboard.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, representation := range snapshot.Items[0].Representations {
		if representation.Format == "CF_UNICODETEXT" {
			return string(representation.Data), snapshot.Generation
		}
	}
	t.Fatalf("no CF_UNICODETEXT in %+v", snapshot)
	return "", ""
}

// A receipt names the clipboard's generation after the write, so a follow-up write with it is
// not refused as changed. Closing a clipboard that holds text moves the sequence number on.
func TestWindowsWriteReceiptIsCurrent(t *testing.T) {
	ctx := context.Background()
	clipboard := nativeClipboard(t, "CF_UNICODETEXT")
	generation, err := clipboard.Generation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := clipboard.Write(ctx, WriteRequest{ExpectedGeneration: generation, Items: text("first ✓")})
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	if current, _ := clipboard.Generation(ctx); first.Generation != current {
		t.Fatalf("receipt generation %s, clipboard is at %s", first.Generation, current)
	}
	second, err := clipboard.Write(ctx, WriteRequest{ExpectedGeneration: first.Generation, Items: text("second")})
	if err != nil {
		t.Fatalf("write with the first receipt: %v", err)
	}
	value, read := readText(t, clipboard)
	if value != "second" || read != second.Generation {
		t.Fatalf("read %q at %s, want %q at %s", value, read, "second", second.Generation)
	}
	if _, err := clipboard.Write(ctx, WriteRequest{ExpectedGeneration: first.Generation, Items: text("late")}); !errors.Is(err, ErrChanged) {
		t.Fatalf("write with a used receipt: %v, want ErrChanged", err)
	}
	cleared, err := clipboard.Clear(ctx, second.Generation)
	if err != nil {
		t.Fatalf("clear with the second receipt: %v", err)
	}
	if current, _ := clipboard.Generation(ctx); cleared.Generation != current {
		t.Fatalf("clear receipt generation %s, clipboard is at %s", cleared.Generation, current)
	}
	if _, err := clipboard.Read(ctx); !errors.Is(err, ErrDenied) {
		t.Fatalf("read of a cleared clipboard: %v, want ErrDenied", err)
	}
}

// A registered format and Unicode text travel together in the one Windows item.
func TestWindowsWriteSeveralFormats(t *testing.T) {
	ctx := context.Background()
	const binary = "Ext.Clipboard.Go.Tests.Binary"
	clipboard := nativeClipboard(t, "CF_UNICODETEXT", binary)
	generation, err := clipboard.Generation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte{0, 1, 2, 3, 254, 255}
	_, err = clipboard.Write(ctx, WriteRequest{ExpectedGeneration: generation, Items: []Item{{Representations: []Representation{
		{Format: "CF_UNICODETEXT", Data: []byte("both")},
		{Format: binary, Data: payload},
	}}}})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	snapshot, err := clipboard.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := map[string][]byte{}
	for _, representation := range snapshot.Items[0].Representations {
		got[representation.Format] = representation.Data
	}
	if string(got["CF_UNICODETEXT"]) != "both" {
		t.Fatalf("text %q", got["CF_UNICODETEXT"])
	}
	// GlobalSize may report more than was allocated: the payload must lead.
	if data := got[binary]; len(data) < len(payload) || string(data[:len(payload)]) != string(payload) {
		t.Fatalf("binary %v, want a prefix of %v", data, payload)
	}
	if len(got) != 2 {
		t.Fatalf("read %d formats, want the 2 allowed", len(got))
	}
}
