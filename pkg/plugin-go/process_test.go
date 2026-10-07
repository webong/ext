//go:build ext_cengine && cgo && (darwin || linux)

package goengine

import (
	"context"
	"errors"
	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/plugintest"
	"os"
	"path/filepath"
	"testing"
)

func TestProcessConfiguration(t *testing.T) {
	for _, options := range []ProcessOptions{{Executable: "relative"}, {Executable: "/bin/echo", Environment: []string{"A=one", "A=two"}}, {Executable: "/bin/echo", Environment: []string{"missing"}}, {Executable: "/bin/echo", Arguments: []string{"nul\x00"}}} {
		h, err := NewProcess(options, plugintest.Descriptor(), allow, allow)
		if h != nil {
			h.Destroy()
		}
		if !errors.Is(err, plugin.ErrInvalid) {
			t.Fatalf("%#v: %v", options, err)
		}
	}
	guest := os.Getenv("EXT_CENGINE_GUEST")
	if guest == "" {
		t.Skip("process fixture not supplied")
	}
	wrapper := filepath.Join(t.TempDir(), "launch")
	data := []byte("#!/bin/sh\n[ \"$1\" = expected ] || exit 2\n[ \"$EXT_PLUGIN_VALUE\" = selected ] || exit 3\n[ -z \"$EXT_PLUGIN_PARENT\" ] || exit 4\nexec \"$2\"\n")
	if err := os.WriteFile(wrapper, data, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXT_PLUGIN_PARENT", "must-not-inherit")
	h, err := NewProcess(ProcessOptions{Executable: wrapper, Arguments: []string{"expected", guest}, Environment: []string{"EXT_PLUGIN_VALUE=selected"}}, plugintest.Descriptor(), allow, allow)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Destroy()
	if err = h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = h.CallRaw(context.Background(), request("echo")); err != nil {
		t.Fatal(err)
	}
}
