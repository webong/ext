package adapter

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/package"
)

// An adapter is an ext package: a plugin.json template in a source directory,
// sealed (digests, platform, revision) when it is installed or packaged.

// scriptSource writes a shell-style adapter source with a unix launcher, a
// Windows launcher, a helper binary and a nested asset. Its template lists the
// two launchers; the helper and the asset are not declared.
func scriptSource(t *testing.T, root, name string) string {
	t.Helper()
	directory := fixtureAdapter(t, root, name, "computer")
	path := filepath.Join(directory, ManifestFileName)
	text, err := readText(path)
	if err != nil {
		t.Fatal(err)
	}
	text = append(text, "executable_windows = \"ctx-"+name+".ps1\"\n"...)
	if err := writeText(path, text, 0o644); err != nil {
		t.Fatal(err)
	}
	for file, body := range map[string]string{
		"ctx-" + name + ".ps1":   "exit 0\n",
		"ctx-" + name + "-share": "#!/bin/sh\nexit 0\n",
		"assets/policy.json":     "{}\n",
	} {
		full := filepath.Join(directory, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(file, "-share") {
			mode = 0o755
		}
		if err := os.WriteFile(full, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

func artifactPaths(pm packagekit.Manifest) map[string]packagekit.Artifact {
	paths := map[string]packagekit.Artifact{}
	for _, artifact := range pm.Artifacts {
		paths[artifact.Path] = artifact
	}
	return paths
}

func TestSealListsEveryFileAndStampsThePlatform(t *testing.T) {
	directory := scriptSource(t, t.TempDir(), "echo")
	pm, err := Seal(directory, Target{OS: "linux", Arch: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	paths := artifactPaths(pm)
	for _, want := range []string{"ctx-echo", "ctx-echo-share", "assets/policy.json"} {
		if _, ok := paths[want]; !ok {
			t.Errorf("sealed manifest does not list %s: %v", want, paths)
		}
	}
	if _, ok := paths["ctx-echo.ps1"]; ok {
		t.Error("the Windows launcher was listed in a linux package")
	}
	if launcher := paths["ctx-echo"]; launcher.OS != "linux" || launcher.Arch != "amd64" {
		t.Errorf("launcher platform %s/%s", launcher.OS, launcher.Arch)
	}
	if helper := paths["ctx-echo-share"]; helper.OS != "" {
		t.Errorf("a helper is not a launcher but has platform %q", helper.OS)
	}
	if want := "sha256:" + paths["ctx-echo"].SHA256; pm.Descriptor.Identity.Revision != want {
		t.Errorf("revision %q, want %q", pm.Descriptor.Identity.Revision, want)
	}
	if !sealed(pm) {
		t.Fatal("not sealed")
	}
	// the file on disk is what was returned, and it verifies as an installed package
	onDisk, err := readPackage(directory)
	if err != nil || !sealed(onDisk) {
		t.Fatalf("%v sealed=%v", err, sealed(onDisk))
	}
	// the dropped launcher is still in the directory, so the directory is not exact
	if err := verifyPackage(directory, onDisk); err == nil || !strings.Contains(err.Error(), "ctx-echo.ps1") {
		t.Fatalf("an unlisted file was not reported: %v", err)
	}
	if err := os.Remove(filepath.Join(directory, "ctx-echo.ps1")); err != nil {
		t.Fatal(err)
	}
	if err := verifyPackage(directory, onDisk); err != nil {
		t.Fatal(err)
	}
	// a second seal is refused, and a change after sealing is found
	if _, err := Seal(directory, Target{OS: "linux", Arch: "amd64"}); err == nil {
		t.Fatal("a sealed manifest was sealed again")
	}
	if err := os.WriteFile(filepath.Join(directory, "assets", "policy.json"), []byte("{\"x\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyPackage(directory, onDisk); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatalf("a changed file verified: %v", err)
	}
}

func TestSealForWindowsKeepsTheWindowsLauncher(t *testing.T) {
	directory := scriptSource(t, t.TempDir(), "echo")
	pm, err := Seal(directory, Target{OS: "windows", Arch: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	paths := artifactPaths(pm)
	if launcher, ok := paths["ctx-echo.ps1"]; !ok || launcher.OS != "windows" || launcher.Arch != "arm64" {
		t.Fatalf("Windows launcher %+v in %v", launcher, paths)
	}
	if _, ok := paths["ctx-echo"]; ok {
		t.Error("the unix launcher was listed in a windows package")
	}
	values, err := valuesFromPackage(pm)
	if err != nil {
		t.Fatal(err)
	}
	// a package that targets Windows only still has a default launcher
	if values["executable_windows"] != "ctx-echo.ps1" || values["executable"] != "ctx-echo.ps1" {
		t.Fatalf("launchers %q %q", values["executable"], values["executable_windows"])
	}
}

func TestInstallSealsForThisPlatformAndKeepsHelpers(t *testing.T) {
	root := t.TempDir()
	source := scriptSource(t, filepath.Join(root, "source"), "echo")
	store := NewStore(filepath.Join(root, "installed"))
	installed, err := store.Install(source)
	if err != nil {
		t.Fatal(err)
	}
	if !sealed(installed.Package) {
		t.Fatal("an installed adapter must be sealed")
	}
	if err := verifyPackage(installed.Directory, installed.Package); err != nil {
		t.Fatal(err)
	}
	for _, kept := range []string{"ctx-echo-share", filepath.Join("assets", "policy.json")} {
		if _, err := os.Stat(filepath.Join(installed.Directory, kept)); err != nil {
			t.Errorf("installed adapter lacks %s: %v", kept, err)
		}
	}
	_, err = os.Stat(filepath.Join(installed.Directory, "ctx-echo.ps1"))
	if runtime.GOOS == "windows" {
		if err != nil {
			t.Errorf("the Windows launcher was not installed on Windows: %v", err)
		}
	} else if !os.IsNotExist(err) {
		t.Errorf("the Windows launcher was installed on %s: %v", runtime.GOOS, err)
	}
	// the source is untouched, and it is still a template
	original, err := readPackage(source)
	if err != nil || sealed(original) {
		t.Fatalf("the source directory was changed: %v sealed=%v", err, sealed(original))
	}
	// a directory copied into the home by hand keeps its template: it loads, and
	// the trust digest of its files covers it like any installed adapter
	handCopied := filepath.Join(store.Home, "copied")
	if err := copyDirectory(scriptSource(t, filepath.Join(root, "other"), "copied"), handCopied); err != nil {
		t.Fatal(err)
	}
	checks, err := store.Verify()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"copied", "echo"} {
		if c := checkByName(t, checks, name); c.Err != nil || c.State != TrustUntrusted {
			t.Fatalf("%s: %+v", name, c)
		}
	}
	copied, err := store.Load("copied")
	if err != nil || sealed(copied.Package) {
		t.Fatalf("hand-copied adapter: %v sealed=%v", err, sealed(copied.Package))
	}
	if err := store.Trust(copied); err != nil {
		t.Fatal(err)
	}
	if state, err := store.TrustState(copied); err != nil || state != TrustTrusted {
		t.Fatalf("trust state %v %v", state, err)
	}
}

func TestLoaderRejectsMalformedPackages(t *testing.T) {
	edit := func(t *testing.T, change func(pm *packagekit.Manifest)) error {
		t.Helper()
		directory := fixtureAdapter(t, t.TempDir(), "echo", "computer")
		pm, err := readPackage(directory)
		if err != nil {
			t.Fatal(err)
		}
		change(&pm)
		encoded, err := json.Marshal(pm)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, ManifestFileName), encoded, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err = LoadDirectory(directory)
		return err
	}
	for name, change := range map[string]func(pm *packagekit.Manifest){
		"an unknown metadata key": func(pm *packagekit.Manifest) {
			pm.Metadata = map[string]json.RawMessage{MetadataKey: json.RawMessage(`{"colour":"red"}`)}
		},
		"the removed kind key": func(pm *packagekit.Manifest) {
			pm.Metadata = map[string]json.RawMessage{MetadataKey: json.RawMessage(`{"kind":"selector"}`)}
		},
		"a metadata value of the wrong type": func(pm *packagekit.Manifest) {
			pm.Metadata = map[string]json.RawMessage{MetadataKey: json.RawMessage(`{"surfaces":"shell"}`)}
		},
		"a launcher that is not argv": func(pm *packagekit.Manifest) { pm.Entrypoints[0].Runtime = "process" },
		"a launcher with another protocol": func(pm *packagekit.Manifest) {
			pm.Entrypoints[0].Protocols = []string{plugin.APIVersion}
		},
		"an entrypoint ctx does not define": func(pm *packagekit.Manifest) { pm.Entrypoints[0].Name = "default" },
		"a contract ctx does not define for adapters": func(pm *packagekit.Manifest) {
			pm.Descriptor.Contracts = append(pm.Descriptor.Contracts, plugin.Contract{
				ContractRef: plugin.ContractRef{Name: "other.thing", Version: "1"},
				Operations:  []plugin.Operation{{Name: "go"}},
			})
		},
		"no ext.adapter contract": func(pm *packagekit.Manifest) {
			pm.Descriptor.Contracts[0].Name = "other.thing"
		},
		"another adapter API version": func(pm *packagekit.Manifest) {
			pm.Descriptor.Contracts[0].Version = "3.0"
		},
		"digests on some artifacts only": func(pm *packagekit.Manifest) {
			pm.Artifacts = append(pm.Artifacts, packagekit.Artifact{Name: "extra", Path: "extra", SHA256: strings.Repeat("a", 64)})
		},
		"a revision without digests": func(pm *packagekit.Manifest) { pm.Descriptor.Identity.Revision = "r1" },
	} {
		if err := edit(t, change); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	// the old file name is not read, and the error says what to do
	old := fixtureAdapter(t, t.TempDir(), "legacy", "computer")
	if err := os.Rename(filepath.Join(old, ManifestFileName), filepath.Join(old, "adapter.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDirectory(old); err == nil || !strings.Contains(err.Error(), "adapter.toml is no longer read") {
		t.Fatalf("an adapter.toml directory: %v", err)
	}
}

func TestManifestRoundTripsEveryField(t *testing.T) {
	directory := fixtureAdapter(t, t.TempDir(), "browser_x", "browser")
	path := filepath.Join(directory, ManifestFileName)
	text, err := readText(path)
	if err != nil {
		t.Fatal(err)
	}
	text = append(text, []byte(`capabilities = "list,validate,open,doctor,share"
browser_share = "cookie.list,cookie.query"
browser_management = "extension.targets,session.endpoint,session.targets"
browser_query_priority = "80"
browser_query_auto = "false"
share_spaces = "browser"
display_name = "Browser X"
supports = "a,b"
override_env = "BROWSER_X_HOME"
dependencies_darwin = "credential:keychain@2.0"
dependencies_linux = "credential:secret_service@2.0"
`)...)
	if err := writeText(path, text, 0o644); err != nil {
		t.Fatal(err)
	}
	pm, err := readPackage(directory)
	if err != nil {
		t.Fatal(err)
	}
	contracts := map[string][]string{}
	for _, contract := range pm.Descriptor.Contracts {
		for _, operation := range contract.Operations {
			contracts[contract.Name+"@"+contract.Version] = append(contracts[contract.Name+"@"+contract.Version], operation.Name)
		}
	}
	if got := strings.Join(contracts["ext.adapter.browser.share@1"], ","); got != "cookie.list,cookie.query" {
		t.Errorf("browser share contract %q", got)
	}
	if got := strings.Join(contracts["ext.adapter.browser.management@1"], ","); got != "extension.targets,session.endpoint,session.targets" {
		t.Errorf("browser management contract %q", got)
	}
	var metadata map[string]any
	if err := json.Unmarshal(pm.Metadata[MetadataKey], &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["browser_query_priority"] != float64(80) || metadata["browser_query_auto"] != false || metadata["display_name"] != "Browser X" {
		t.Errorf("metadata %v", metadata)
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		loaded, err := LoadDirectoryForOS(directory, goos)
		if err != nil {
			t.Fatalf("%s: %v", goos, err)
		}
		m := loaded.Manifest
		if m.DisplayName != "Browser X" || m.BrowserQueryPriority != 80 || m.BrowserQueryAuto || !contains(m.BrowserShare, "cookie.query") ||
			!loaded.HasBrowserManagement("session.endpoint") || !loaded.HasBrowserShare("cookie.list") || !contains(m.OverrideEnv, "BROWSER_X_HOME") {
			t.Fatalf("%s: %+v", goos, m)
		}
		want := map[string]string{"darwin": "keychain", "linux": "secret_service", "windows": ""}[goos]
		if got := m.Dependencies["credential"].Adapter; got != want {
			t.Errorf("%s dependency %q, want %q", goos, got, want)
		}
	}
}

// readArchive returns the files of a zip by name.
func readArchive(t *testing.T, path string) map[string][]byte {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	files := map[string][]byte{}
	for _, entry := range reader.File {
		input, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(input)
		input.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name] = data
	}
	return files
}

// writeArchive writes files as a zip.
func writeArchive(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, data := range files {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o755)
		output, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := output.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPackageAndInstallArchive(t *testing.T) {
	root := t.TempDir()
	source := scriptSource(t, filepath.Join(root, "source"), "echo")
	archive := filepath.Join(root, "dist", "echo.ctxadapter")
	if err := PackageDirectory(source, archive, runtime.GOOS, runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
	files := readArchive(t, archive)
	if _, ok := files["ctx-package.json"]; ok {
		t.Error("the archive still carries a second metadata record")
	}
	var pm packagekit.Manifest
	if err := plugin.Decode(files[ManifestFileName], &pm); err != nil || !sealed(pm) {
		t.Fatalf("archive manifest: %v sealed=%v", err, sealed(pm))
	}
	info, err := archiveInfo(pm)
	if err != nil || info.Name != "echo" || info.OS != runtime.GOOS || info.Arch != runtime.GOARCH {
		t.Fatalf("archive info %+v %v", info, err)
	}
	for _, name := range []string{"ctx-echo-share", "assets/policy.json"} {
		if _, ok := files[name]; !ok {
			t.Errorf("archive lacks %s", name)
		}
	}
	if _, ok := files["ctx-echo.ps1"]; ok && runtime.GOOS != "windows" {
		t.Error("archive carries the Windows launcher for another platform")
	}
	// the source directory was not sealed by packaging it
	if original, err := readPackage(source); err != nil || sealed(original) {
		t.Fatalf("packaging changed the source: %v sealed=%v", err, sealed(original))
	}

	store := NewStore(filepath.Join(root, "installed"))
	installed, err := store.InstallSource(archive, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyPackage(installed.Directory, installed.Package); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(installed.ExecutablePathForOS(runtime.GOOS)); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		t.Fatalf("launcher %v %v", info, err)
	}

	// an archive for another platform is refused
	other := "arm64"
	if runtime.GOARCH == "arm64" {
		other = "amd64"
	}
	foreign := filepath.Join(root, "dist", "foreign.ctxadapter")
	if err := PackageDirectory(source, foreign, runtime.GOOS, other); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(filepath.Join(root, "again")).InstallSource(foreign, ""); err == nil || !strings.Contains(err.Error(), "targets") {
		t.Fatalf("an archive for %s/%s was accepted: %v", runtime.GOOS, other, err)
	}

	// a file changed after packaging is refused
	tampered := readArchive(t, archive)
	tampered["assets/policy.json"] = []byte("{\"evil\":true}\n")
	tamperedPath := filepath.Join(root, "dist", "tampered.ctxadapter")
	writeArchive(t, tamperedPath, tampered)
	if _, err := NewStore(filepath.Join(root, "again")).InstallSource(tamperedPath, ""); err == nil {
		t.Fatal("a tampered archive was installed")
	}

	// a file the manifest does not list is refused
	extra := readArchive(t, archive)
	extra["backdoor"] = []byte("#!/bin/sh\n")
	extraPath := filepath.Join(root, "dist", "extra.ctxadapter")
	writeArchive(t, extraPath, extra)
	if _, err := NewStore(filepath.Join(root, "again")).InstallSource(extraPath, ""); err == nil {
		t.Fatal("an archive with an unlisted file was installed")
	}

	// the platform index names the archive by platform and digest
	index := filepath.Join(root, "dist", "echo.ctxadapter.json")
	if err := WriteIndex(index, []string{archive, foreign}); err != nil {
		t.Fatal(err)
	}
	fromIndex, err := NewStore(filepath.Join(root, "viaindex")).InstallSource(index, "")
	if err != nil || fromIndex.Manifest.Name != "echo" {
		t.Fatalf("install through the index: %v", err)
	}
}

func TestBuildGoPackage(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("needs the go command")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"go.mod":      "module example.invalid/gotest\n\ngo 1.21\n",
		"main.go":     "package main\n\nfunc main() {}\n",
		"policy.json": "{}\n",
	} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteKeyValueManifest(source, `api_version = "2.0"
name = "gotest"
runtime = "computer"
surfaces = "shell"
executable = "ctx-gotest"
executable_windows = "ctx-gotest.exe"
capabilities = "list,validate,run,doctor"
commands = "gotest"
package_files = "policy.json"
`); err != nil {
		t.Fatal(err)
	}
	for number, target := range []Target{{runtime.GOOS, runtime.GOARCH}, {"windows", "amd64"}} {
		archive, err := BuildGoPackage(GoBuildOptions{Source: source, OS: target.OS, Arch: target.Arch, Output: filepath.Join(root, "dist", strconv.Itoa(number)+"-"+target.OS+"-"+target.Arch+".ctxadapter")})
		if err != nil {
			t.Fatalf("%s/%s: %v", target.OS, target.Arch, err)
		}
		files := readArchive(t, archive)
		var pm packagekit.Manifest
		if err := plugin.Decode(files[ManifestFileName], &pm); err != nil || !sealed(pm) {
			t.Fatalf("%s/%s manifest: %v", target.OS, target.Arch, err)
		}
		launcher := "ctx-gotest"
		if target.OS == "windows" {
			launcher = "ctx-gotest.exe"
		}
		paths := artifactPaths(pm)
		if built, ok := paths[launcher]; !ok || built.OS != target.OS || built.Arch != target.Arch {
			t.Errorf("%s/%s: launcher %+v in %v", target.OS, target.Arch, built, paths)
		}
		if _, ok := paths["policy.json"]; !ok {
			t.Errorf("%s/%s: package_files entry not shipped: %v", target.OS, target.Arch, paths)
		}
		if _, ok := paths["main.go"]; ok {
			t.Errorf("%s/%s: Go source shipped", target.OS, target.Arch)
		}
		if _, ok := files[launcher]; !ok {
			t.Errorf("%s/%s: archive lacks the built launcher", target.OS, target.Arch)
		}
		if target.OS == runtime.GOOS && target.Arch == runtime.GOARCH {
			installed, err := NewStore(filepath.Join(root, "installed", strconv.Itoa(number))).InstallSource(archive, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := verifyPackage(installed.Directory, installed.Package); err != nil {
				t.Fatal(err)
			}
		}
	}
	// a sealed package is not a source: there is nothing to build
	sealedPackage := scriptSource(t, filepath.Join(root, "sealed"), "done")
	if _, err := Seal(sealedPackage, Target{OS: runtime.GOOS, Arch: runtime.GOARCH}); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildGoPackage(GoBuildOptions{Source: sealedPackage}); err == nil {
		t.Fatal("a sealed package was built")
	}
}
