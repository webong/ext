package inference_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	ginference "github.com/webong/ext/pkg/ext-go/inference"
)

func TestPackageExecutableInstallsAndResolves(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "guest")
	if err := os.WriteFile(binary, []byte("guest executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	packageDir := filepath.Join(root, "package")
	manifest, err := ginference.PackageExecutable(ginference.ExecutablePackage{ID: "sample", Version: "1.0.0", Binary: binary, OutputDir: packageDir})
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.New(filepath.Join(root, "installed"), store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := s.Install(packageDir)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := installed.Digest()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := s.ResolveEntrypoint("sample", ginference.ContractName, ginference.ContractVersion, "inference.provider/v1", digest)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Path == "" || selected.Package.Version() != manifest.Version() {
		t.Fatalf("wrong selection: %+v", selected)
	}
	if _, err := ginference.PackageExecutable(ginference.ExecutablePackage{ID: "sample", Version: "1.0.0", Binary: binary, OutputDir: packageDir}); err == nil {
		t.Fatal("overwrote existing package")
	}
}

func TestPackageExecutableCrossTarget(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "guest")
	if err := os.WriteFile(binary, []byte("cross target"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, err := ginference.PackageExecutable(ginference.ExecutablePackage{ID: "cross", Version: "1", Binary: binary, OutputDir: filepath.Join(root, "package"), OS: "windows", Arch: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Artifacts[0].OS != "windows" || manifest.Artifacts[0].Arch != "amd64" || manifest.Artifacts[0].Path != "provider.exe" {
		t.Fatalf("wrong target artifact: %+v", manifest.Artifacts[0])
	}
	if runtime.GOOS != "windows" {
		s, err := store.New(filepath.Join(root, "installed"), store.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		installed, err := s.Install(filepath.Join(root, "package"))
		if err != nil {
			t.Fatal(err)
		}
		digest, err := installed.Digest()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ResolveEntrypoint("cross", ginference.ContractName, ginference.ContractVersion, "inference.provider/v1", digest); err == nil {
			t.Fatal("cross-target executable selected on wrong OS")
		}
	}
}
