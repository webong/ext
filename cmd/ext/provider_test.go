package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProviderThroughCLibrary(t *testing.T) {
	libraryPath := os.Getenv("EXT_ACCEPTANCE_LIBRARY")
	if libraryPath == "" {
		t.Skip("run scripts/package-acceptance.sh for the required C engine acceptance case")
	}
	home, bin, source, outside := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	ext, host, guest := filepath.Join(bin, "ext"+suffix), filepath.Join(bin, "consumer"+suffix), filepath.Join(source, "provider"+suffix)
	goBuild(t, ext, ".")
	stageAcceptanceEngine(t, bin, libraryPath)
	goBuild(t, host, "./testdata/providerhost")
	goBuild(t, guest, "./testdata/provider")
	contracts := []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.value", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}
	manifest, err := packagekit.ExecutableManifest("example/provider", "1.0.0", contracts, guest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Descriptor.Identity.Revision = "provider-v1"
	if err := store.WriteManifest(source, manifest); err != nil {
		t.Fatal(err)
	}
	run := func(binary string, want int, contains string, args ...string) {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Dir = outside
		cmd.Env = acceptanceEnv(home)
		output, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				code = e.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if code != want || !strings.Contains(string(output), contains) {
			t.Fatalf("%s %v: code %d output %s; want %d %q", binary, args, code, output, want, contains)
		}
	}
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	for _, name := range []string{store.ManifestName, filepath.Base(guest)} {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0700)
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "provider.zip")
	if err := os.WriteFile(archivePath, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	pin := fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
	run(ext, 1, "", "package", "install", archivePath, "--sha256", strings.Repeat("0", 64))
	if _, err := os.Stat(filepath.Join(home, "packages", "example%2fprovider")); !os.IsNotExist(err) {
		t.Fatalf("failed archive published a package: %v", err)
	}
	indexPath := filepath.Join(filepath.Dir(archivePath), "index.json")
	index := map[string]any{"format_version": 1, "name": "example/provider", "packages": map[string]any{runtime.GOOS + "/" + runtime.GOARCH: map[string]string{"url": "provider.zip", "sha256": pin}}}
	indexBytes, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, indexBytes, 0600); err != nil {
		t.Fatal(err)
	}
	run(ext, 0, "add source example", "source", "add", "example", indexPath)
	run(ext, 1, "already exists", "source", "add", "example", indexPath)
	run(ext, 0, "example/provider", "source", "list")
	run(ext, 0, pin, "source", "inspect", "example")
	// A failed refresh leaves the previous snapshot usable and never approves code.
	if err := os.WriteFile(indexPath, []byte(`{"format_version":99}`), 0600); err != nil {
		t.Fatal(err)
	}
	run(ext, 1, "invalid", "source", "refresh", "example")
	run(ext, 0, "installed", "package", "install", "--source", "example")
	if err := os.WriteFile(indexPath, indexBytes, 0600); err != nil {
		t.Fatal(err)
	}
	run(ext, 0, "refresh source example", "source", "refresh", "example")
	changedIndex := strings.Replace(string(indexBytes), "example/provider", "example/other", 1)
	if err := os.WriteFile(indexPath, []byte(changedIndex), 0600); err != nil {
		t.Fatal(err)
	}
	run(ext, 1, "identity changed", "source", "refresh", "example")
	run(ext, 0, "example/provider", "source", "inspect", "example")
	run(ext, 0, "add source wrong", "source", "add", "wrong", indexPath)
	run(ext, 1, "", "package", "install", "--source", "wrong")
	if _, err := os.Stat(filepath.Join(home, "packages", "example%2fother")); !os.IsNotExist(err) {
		t.Fatalf("mismatched identity published: %v", err)
	}
	run(ext, 0, "remove source wrong", "source", "remove", "wrong")

	run(ext, 0, "remove source example", "source", "remove", "example")
	run(ext, 0, "trusted=false", "package", "verify", "example/provider")
	run(ext, 1, "not found", "package", "install", "--source", "example")
	installedStore, err := store.New(filepath.Join(home, "packages"), store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := installedStore.Receipt("example/provider")
	if err != nil || receipt.SHA256 != pin {
		t.Fatalf("archive receipt: %+v %v", receipt, err)
	}
	run(host, 1, "not trusted", libraryPath, home)
	run(ext, 0, "trusted", "package", "trust", "example/provider")
	run(ext, 2, "unknown command", "read")
	run(host, 0, "from-provider", libraryPath, home)
	run(ext, 0, "no longer trusted", "package", "untrust", "example/provider")
	run(host, 1, "not trusted", libraryPath, home)
	run(ext, 0, "trusted", "package", "trust", "example/provider")
	installed := filepath.Join(home, "packages", "example%2fprovider", "provider"+suffix)
	f, err := os.OpenFile(installed, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("tampered")
	closeErr := f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	run(host, 1, "", libraryPath, home)
	run(ext, 0, "removed", "package", "remove", "example/provider")
	run(host, 1, "", libraryPath, home)
	// Migrate a local catalog into pinned sources without activating its packages.
	catalog, err := store.New(t.TempDir(), store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.InstallWithOptions(source, store.CopyOptions{PreserveExecutableArtifacts: true}); err != nil {
		t.Fatal(err)
	}
	nameHash := sha256.Sum256([]byte("example/provider"))
	sourceName := fmt.Sprintf("catalog-%x", nameHash[:8])
	run(ext, 0, sourceName, "source", "import-catalog", catalog.Root())
	run(ext, 1, "", "package", "inspect", "example/provider")
	run(ext, 0, sourceName, "source", "list")
	run(ext, 1, "already exists", "source", "import-catalog", catalog.Root())
	run(ext, 2, "source import-catalog", "package", "add", "example/provider")
	run(ext, 0, "installed", "package", "install", "--source", sourceName)
	run(ext, 0, "trusted=false", "package", "verify", "example/provider")
	run(host, 1, "not trusted", libraryPath, home)
	run(ext, 0, "trusted", "package", "trust", "example/provider")
	run(host, 0, "from-provider", libraryPath, home)
	run(ext, 0, "remove source", "source", "remove", sourceName)
	run(host, 0, "from-provider", libraryPath, home)

	// Resolve and execute a real guest -> host -> provider call, rather than
	// treating a successful dependency plan as invocation evidence.
	dependentDir := t.TempDir()
	dependentBin := filepath.Join(dependentDir, "dependent"+suffix)
	goBuild(t, dependentBin, "./testdata/dependent")
	dependentContracts := []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.dependent", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}
	dependent, err := packagekit.ExecutableManifest("example/dependent", "1.0.0", dependentContracts, dependentBin)
	if err != nil {
		t.Fatal(err)
	}
	dependent.Descriptor.Identity.Revision = "dependent-v1"
	dependent.Descriptor.Reverse = contracts
	dependent.Requires = []packagekit.Dependency{{Contract: contracts[0].ContractRef, Operation: "read"}}
	if err := store.WriteManifest(dependentDir, dependent); err != nil {
		t.Fatal(err)
	}
	run(ext, 0, "installed", "package", "install", dependentDir)
	run(host, 1, "example/dependent not trusted", libraryPath, home, "chain")
	run(ext, 0, "trusted", "package", "trust", "example/dependent")
	run(host, 0, `"via":"dependent"`, libraryPath, home, "chain")
	run(host, 1, "reverse_unavailable", libraryPath, home, "chain-closed")
	run(host, 1, "denied", libraryPath, home, "chain-denied")
	run(host, 0, "from-provider", libraryPath, home, "chain")
	run(ext, 0, "no longer trusted", "package", "untrust", "example/provider")
	run(host, 1, "example/provider not trusted", libraryPath, home, "chain")
	run(ext, 0, "trusted", "package", "trust", "example/provider")
	run(ext, 0, "no longer trusted", "package", "untrust", "example/dependent")
	run(host, 1, "example/dependent not trusted", libraryPath, home, "chain")
	run(ext, 0, "trusted", "package", "trust", "example/dependent")
	run(ext, 0, "removed", "package", "remove", "example/provider", "--force")
	run(host, 1, "not_found", libraryPath, home, "chain")
	run(ext, 0, "installed", "package", "install", source)
	run(host, 1, "not trusted", libraryPath, home, "chain")
	run(ext, 0, "trusted", "package", "trust", "example/provider")
	run(host, 0, "from-provider", libraryPath, home, "chain")

	alternateDir := t.TempDir()
	alternateBin := filepath.Join(alternateDir, "provider"+suffix)
	build := exec.Command("go", "build", "-ldflags=-X main.providerID=example/alternate", "-o", alternateBin, "./testdata/provider")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build alternate: %v: %s", err, output)
	}
	alternate, err := packagekit.ExecutableManifest("example/alternate", "1.0.0", contracts, alternateBin)
	if err != nil {
		t.Fatal(err)
	}
	alternate.Descriptor.Identity.Revision = "provider-v1"
	if err := store.WriteManifest(alternateDir, alternate); err != nil {
		t.Fatal(err)
	}
	run(ext, 0, "installed", "package", "install", alternateDir)
	run(host, 1, "ambiguous", libraryPath, home, "chain")
	run(ext, 0, "removed", "package", "remove", "example/alternate", "--force")
	run(host, 0, "from-provider", libraryPath, home, "chain")

}
