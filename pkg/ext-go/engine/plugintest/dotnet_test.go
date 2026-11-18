package plugintest_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/process"
	"github.com/webong/ext/pkg/ext-go/engine/plugintest"
)

// The .NET guest SDK (pkg/ext-dotnet/ExtIO) must pass the same suite through the
// process backend. EXT_DOTNET_GUEST names the built ExtIO.Conformance.dll and
// EXT_DOTNET the dotnet launcher (default: dotnet on PATH); skipped without the
// guest, which scripts/lang/csharp.sh builds.
func TestDotnetGuest(t *testing.T) {
	guest := os.Getenv("EXT_DOTNET_GUEST")
	if guest == "" {
		t.Skip("set EXT_DOTNET_GUEST to a built ExtIO.Conformance.dll (scripts/lang/csharp.sh test)")
	}
	dotnet := os.Getenv("EXT_DOTNET")
	if dotnet == "" {
		var err error
		if dotnet, err = exec.LookPath("dotnet"); err != nil {
			t.Skip("dotnet not installed")
		}
	}
	dotnet, err := filepath.Abs(dotnet)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(guest)
	descriptor := filepath.Join(dir, "descriptor.json")
	plugintest.Run(t, func(ctx context.Context) (plugin.Backend, error) {
		return process.Start(ctx, process.Command{Path: dotnet, Args: []string{guest, "conformance", descriptor}, Dir: dir})
	})
}
