package wasm

import (
	"context"
	"debug/dwarf"
	"errors"
	"testing"
	"time"
)

// customSection returns a WebAssembly custom section (id 0) called name.
func customSection(name string, payload []byte) []byte {
	leb := func(n int) []byte {
		var out []byte
		for {
			b := byte(n & 0x7f)
			n >>= 7
			if n != 0 {
				out = append(out, b|0x80)
				continue
			}
			return append(out, b)
		}
	}
	body := append(append(leb(len(name)), name...), payload...)
	return append(append([]byte{0}, leb(len(body))...), body...)
}

// debugInfoOfEntries returns the .debug_abbrev and .debug_info sections of one compilation unit
// that holds n childless entries: valid DWARF that says nothing about the code, so a lookup of a
// source line in it reads every entry and finds none.
func debugInfoOfEntries(n int) (abbrev, info []byte) {
	// Abbreviation 1: a compilation unit with children. Abbreviation 2: a variable without any.
	abbrev = []byte{1, 0x11, 1, 0, 0, 2, 0x34, 0, 0, 0, 0}
	body := []byte{4, 0, 0, 0, 0, 0, 4, 1} // version 4, abbreviations at 0, 4-byte addresses, the unit
	for i := 0; i < n; i++ {
		body = append(body, 2)
	}
	body = append(body, 0) // the end of the unit's children
	length := len(body)
	info = append([]byte{byte(length), byte(length >> 8), byte(length >> 16), byte(length >> 24)}, body...)
	return abbrev, info
}

// nestedSpinModule is
//
//	(module
//	  (func (export "_start") (call 1))
//	  (func (call 2))
//	  (func (loop (br 0))))
//
// so closing it interrupts a guest three calls deep.
var nestedSpinModule = []byte{
	0, 97, 115, 109, 1, 0, 0, 0,
	1, 4, 1, 96, 0, 0,
	3, 4, 3, 0, 0, 0,
	7, 10, 1, 6, '_', 's', 't', 'a', 'r', 't', 0, 0,
	10, 19, 3, 4, 0, 16, 1, 11, 4, 0, 16, 2, 11, 7, 0, 3, 64, 12, 0, 11, 11,
}

// TestClosingAGuestDoesNotReadItsDebugInfo holds Close to a bound that does not grow with the
// debug information in the module. wazero stops a running guest by unwinding it with an error,
// and by default looks up a source line in the module's DWARF for every frame it unwinds; a
// lookup reads the entries from the start, so a real guest (a Zig WASI command built with debug
// information) kept Close, and so the session's cleanup, busy for seconds on a loaded runner.
func TestClosingAGuestDoesNotReadItsDebugInfo(t *testing.T) {
	abbrev, info := debugInfoOfEntries(2_000_000)
	module := append(append(append([]byte(nil), nestedSpinModule...),
		customSection(".debug_abbrev", abbrev)...), customSection(".debug_info", info)...)

	// What reading every entry once costs here, which a lookup does for each of the three frames.
	data, err := dwarf.New(abbrev, nil, nil, info, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	entries := 0
	for reader := data.Reader(); ; entries++ {
		entry, err := reader.Next()
		if err != nil || entry == nil {
			break
		}
	}
	scan := time.Since(start)
	if entries < 2_000_000 {
		t.Fatalf("the debug information holds %d entries", entries)
	}

	b, err := Open(context.Background(), module, Options{})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // let the guest reach its loop
	start = time.Now()
	_ = b.Close()
	wait, stop := context.WithTimeout(context.Background(), 60*time.Second)
	defer stop()
	err = b.Wait(wait) // closing a running command ends it with an error
	closed := time.Since(start)
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("a closed guest did not finish")
	}
	if closed >= scan {
		t.Errorf("closing took %v with %v of debug information to read: it is read when the guest is unwound", closed, scan)
	}
}
