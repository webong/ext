package host_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cgoengine/host"
	"github.com/webong/ext/pkg/ext-go/cgoengine/host/store"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/package"
)

func TestReviewedRuntimeLaunchesSourceGuest(t *testing.T) {
	root := t.TempDir()
	s, err := store.New(filepath.Join(root, "installed"), store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	runtimeSource := filepath.Join(root, "runtime")
	if err := os.MkdirAll(filepath.Join(runtimeSource, "vendor"), 0o700); err != nil {
		t.Fatal(err)
	}
	runnerName := "runner"
	if runtime.GOOS == "windows" {
		runnerName += ".exe"
	}
	runner := filepath.Join(runtimeSource, "vendor", runnerName)
	if err := copyRuntimeTestBinary(runner); err != nil {
		t.Fatal(err)
	}
	runtimeManifest, err := packagekit.SourceManifest("example/runtime", "1.0.0", contracts, runtimeSource, []string{"vendor"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteManifest(runtimeSource, runtimeManifest); err != nil {
		t.Fatal(err)
	}
	runtimePackage, err := s.Install(runtimeSource)
	if err != nil {
		t.Fatal(err)
	}
	reviewed, err := runtimePackage.Digest()
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "script.py"), []byte("# source guest\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := packagekit.SourceManifest("example/host", "1.0.0", contracts, source, []string{"script.py"})
	if err != nil {
		t.Fatal(err)
	}
	manifest.Descriptor.Identity.Revision = "child" // TestMain serves this fixed identity.
	if err := store.WriteManifest(source, manifest); err != nil {
		t.Fatal(err)
	}
	installed, err := s.Install(source)
	if err != nil {
		t.Fatal(err)
	}
	options := host.RuntimeOptions{
		Runtime: runtimePackage, ReviewedDigest: reviewed,
		RuntimeArtifact: "vendor", Executable: runnerName, ScriptArtifact: "script.py",
		Options: host.Options{Env: []string{"HOST_TEST_CHILD=1"}},
	}
	if _, err := host.OpenRuntime(context.Background(), installed, host.RuntimeOptions{Runtime: runtimePackage, RuntimeArtifact: "vendor", Executable: runnerName, ScriptArtifact: "script.py"}); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("unreviewed runtime accepted: %v", err)
	}
	process, err := host.OpenRuntime(context.Background(), installed, options)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Args []string `json:"args"`
	}
	if err := process.Call(context.Background(), "example.content.echo", map[string]string{"x": "y"}, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Args) != 1 || !strings.HasSuffix(response.Args[0], "script.py") {
		t.Fatalf("runtime did not receive reviewed script: %+v", response.Args)
	}
	if err := process.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := runtimePackage.ArtifactPath("vendor")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, runnerName), []byte("changed"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := host.OpenRuntime(context.Background(), installed, options); err == nil {
		t.Fatal("tampered runtime was launched")
	}
}

func copyRuntimeTestBinary(target string) error {
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o700)
}
