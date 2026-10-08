package wasm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/plugin"
)

// exportingModule returns a valid WebAssembly module with one empty function
// exported under name, enough to exercise OpenReactor's export checks.
func exportingModule(name string) []byte {
	module := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	module = append(module, 1, 4, 1, 0x60, 0, 0) // type: () -> ()
	module = append(module, 3, 2, 1, 0)          // function 0 has type 0
	section := append([]byte{1, byte(len(name))}, name...)
	section = append(section, 0, 0) // export kind: function, index 0
	module = append(module, 7, byte(len(section)))
	module = append(module, section...)
	return append(module, 10, 4, 1, 2, 0, 0x0b) // body: end
}

func TestOpenReactorRejectsWhatIsNotAReactor(t *testing.T) {
	tests := []struct {
		name   string
		module []byte
		want   error
		text   string
	}{
		{"empty", nil, plugin.ErrInvalid, ""},
		{"not wasm", []byte("not a module"), nil, "compile"},
		{"command", exportingModule("_start"), plugin.ErrUnsupported, "_start"},
		{"missing ABI exports", exportingModule("other"), plugin.ErrUnsupported, "ext_plugin_abi_version"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := OpenReactor(context.Background(), test.module, ReactorOptions{})
			if err == nil {
				t.Fatal("accepted a module that is not a reactor")
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Errorf("error = %v, want %v", err, test.want)
			}
			if test.text != "" && !strings.Contains(err.Error(), test.text) {
				t.Errorf("error = %q, want it to mention %q", err, test.text)
			}
		})
	}
}

func TestOpenReactorHonoursCancellationAndLimits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := OpenReactor(ctx, exportingModule("other"), ReactorOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context: error = %v", err)
	}
	for _, opts := range []ReactorOptions{{MemoryLimitPages: 65537}, {MaxStderrBytes: -1}} {
		if _, err := OpenReactor(context.Background(), exportingModule("other"), opts); !errors.Is(err, plugin.ErrInvalid) {
			t.Errorf("options %+v: error = %v", opts, err)
		}
	}
}
