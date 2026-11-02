package program

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func writeProgram(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0o755)
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
}

func names(candidates []Candidate) []string {
	out := make([]string, len(candidates))
	for i, c := range candidates {
		out[i] = c.Name + "@" + filepath.Base(filepath.Dir(filepath.Dir(c.RealPath)))
	}
	return out
}

func TestScanFindsOnlyNamedExecutables(t *testing.T) {
	root := t.TempDir()
	writeProgram(t, filepath.Join(root, "a", "bin", "php"))
	writeProgram(t, filepath.Join(root, "a", "bin", "other"))
	if err := os.WriteFile(filepath.Join(root, "a", "bin", "plain"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Scan([]string{"php", "plain"}, filepath.Join(root, "a", "bin"))
	if len(got) != 1 || got[0].Name != "php" || !got[0].OnPath || got[0].Source != SourcePath {
		t.Fatalf("Scan = %#v, want one php on PATH", got)
	}
}

func TestScanSkipsRelativeAndEmptyEntries(t *testing.T) {
	root := t.TempDir()
	writeProgram(t, filepath.Join(root, "rel", "php"))
	t.Chdir(root)
	list := strings.Join([]string{"", "rel", "."}, string(os.PathListSeparator))
	if got := Scan([]string{"php"}, list); len(got) != 0 {
		t.Fatalf("Scan followed a relative entry: %#v", got)
	}
}

func TestScanReportsEachDistinctFileAndCollapsesLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need privileges on Windows")
	}
	root := t.TempDir()
	writeProgram(t, filepath.Join(root, "v83", "bin", "php"))
	writeProgram(t, filepath.Join(root, "v81", "bin", "php"))
	link := filepath.Join(root, "shim", "php")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "v83", "bin", "php"), link); err != nil {
		t.Fatal(err)
	}
	list := strings.Join([]string{filepath.Join(root, "shim"), filepath.Join(root, "v83", "bin"), filepath.Join(root, "v81", "bin")}, string(os.PathListSeparator))
	got := Scan([]string{"php"}, list)
	if want := []string{"php@v83", "php@v81"}; !reflect.DeepEqual(names(got), want) {
		t.Fatalf("Scan = %v, want %v (PATH order, link collapsed into its target)", names(got), want)
	}
	if got[0].Path != link {
		t.Fatalf("first path = %s, want the link the user would type: %s", got[0].Path, link)
	}
}

func TestMergeJoinsByRealPathAndAttributesInstallers(t *testing.T) {
	root := t.TempDir()
	onPath := filepath.Join(root, "bin", "php")
	keg := filepath.Join(root, "keg81", "bin", "php")
	writeProgram(t, onPath)
	writeProgram(t, keg)
	scanned := Scan([]string{"php"}, filepath.Dir(onPath))
	if runtime.GOOS == "windows" {
		onPath += ".exe"
		keg += ".exe"
	}
	merged := Merge(scanned, map[string][]Candidate{
		"homebrew": {
			{Name: "php", Path: onPath, Version: "8.3.1", Attributes: map[string]any{"formula": "php"}},
			{Name: "php", Path: keg, Version: "8.1.2"},
			{Name: "php", Path: filepath.Join(root, "gone", "php"), Version: "7.4"},
		},
		"asdf": {{Name: "php", Path: onPath}},
	})
	if len(merged) != 2 {
		t.Fatalf("Merge = %#v, want the on-PATH file and the keg (the missing file dropped)", merged)
	}
	var onPathResult, kegResult Candidate
	for _, c := range merged {
		if strings.Contains(c.RealPath, "keg81") {
			kegResult = c
		} else {
			onPathResult = c
		}
	}
	if !onPathResult.OnPath || onPathResult.Source != SourceInstaller || !reflect.DeepEqual(onPathResult.Installers, []string{"asdf", "homebrew"}) || onPathResult.Version != "8.3.1" {
		t.Fatalf("on-PATH result = %#v", onPathResult)
	}
	if kegResult.OnPath || kegResult.Source != SourceInstaller || !reflect.DeepEqual(kegResult.Installers, []string{"homebrew"}) {
		t.Fatalf("keg result = %#v, want an installer-only program not on PATH", kegResult)
	}
}

func TestMergeLeavesUnclaimedProgramsAsPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bin", "node")
	writeProgram(t, path)
	merged := Merge(Scan([]string{"node"}, filepath.Dir(path)), nil)
	if len(merged) != 1 || merged[0].Source != SourcePath || len(merged[0].Installers) != 0 {
		t.Fatalf("Merge = %#v", merged)
	}
}

func TestInspectRejectsRelativeAndNonExecutable(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "data")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Inspect("data", file); ok && runtime.GOOS != "windows" {
		t.Fatal("a non-executable file was accepted")
	}
	if _, ok := Inspect("php", "bin/php"); ok {
		t.Fatal("a relative path was accepted")
	}
}

func TestForCommands(t *testing.T) {
	in := []Candidate{{Name: "php"}, {Name: "node"}, {Name: "php"}}
	if got := ForCommands(in, []string{"php"}); len(got) != 2 {
		t.Fatalf("ForCommands = %#v", got)
	}
}

func envOf(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestExpandRoot(t *testing.T) {
	env := envOf(map[string]string{"HOME": "/home/u", "NVM_DIR": "/opt/nvm", "EMPTY": ""})
	for pattern, want := range map[string]string{
		"/usr/lib/x/*/bin":                  "/usr/lib/x/*/bin",
		"~/.nvm/versions/node/*/bin":        "/home/u/.nvm/versions/node/*/bin",
		"${NVM_DIR:-~/.nvm}/versions/*/bin": "/opt/nvm/versions/*/bin",
		"${MISSING:-~/.nvm}/versions/*/bin": "/home/u/.nvm/versions/*/bin",
		"${EMPTY:-/fallback}/bin":           "/fallback/bin",
		"$NVM_DIR/v/*":                      "/opt/nvm/v/*",
	} {
		got, ok := ExpandRoot(pattern, env)
		if !ok || got != want {
			t.Errorf("ExpandRoot(%q) = %q, %v; want %q", pattern, got, ok, want)
		}
	}
	for _, pattern := range []string{"relative/bin", "$MISSING/bin", "${MISSING}/bin", "${NVM_DIR", "$/bin", "~user/bin"} {
		if got, ok := ExpandRoot(pattern, env); ok {
			t.Errorf("ExpandRoot(%q) = %q, want it refused", pattern, got)
		}
	}
	if _, ok := ExpandRoot("~/x", envOf(nil)); ok {
		t.Error("~ without a home directory must be refused")
	}
	if runtime.GOOS != "windows" {
		got, _ := ExpandRoot("$ODD/bin", envOf(map[string]string{"ODD": "/a[b]*/c"}))
		if got != `/a\[b]\*/c/bin` {
			t.Errorf("a value from the environment must be literal, got %q", got)
		}
	}
}

func TestScanRoots(t *testing.T) {
	root := t.TempDir()
	writeProgram(t, filepath.Join(root, "versions", "v20", "bin", "node"))
	writeProgram(t, filepath.Join(root, "versions", "v22", "bin", "node"))
	writeProgram(t, filepath.Join(root, "versions", "v22", "bin", "npm"))
	if err := os.WriteFile(filepath.Join(root, "versions", "v18-notes"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := envOf(map[string]string{"NVM_DIR": root})
	found := ScanRoots([]string{"node", "missing"}, []string{"${NVM_DIR:-~/.nvm}/versions/*/bin", "relative/*"}, env)
	if len(found) != 2 {
		t.Fatalf("found %#v, want the two node files", found)
	}
	for _, candidate := range found {
		if candidate.Name != "node" || candidate.Source != SourceInstaller || candidate.OnPath || candidate.Version != "" {
			t.Errorf("candidate = %#v", candidate)
		}
	}
	if found[0].Path >= found[1].Path {
		t.Errorf("results must be ordered: %v, %v", found[0].Path, found[1].Path)
	}
	// With nothing configured nothing is invented.
	if got := ScanRoots([]string{"node"}, []string{"${NVM_DIR}/versions/*/bin"}, envOf(nil)); len(got) != 0 {
		t.Errorf("unset variable matched %#v", got)
	}
	// Joined with a PATH scan, a file both found is one candidate that names the installer.
	onPath := filepath.Join(root, "versions", "v22", "bin")
	merged := Merge(Scan([]string{"node"}, onPath), map[string][]Candidate{"nvm": found})
	if len(merged) != 2 {
		t.Fatalf("merged = %#v", merged)
	}
	for _, candidate := range merged {
		if len(candidate.Installers) != 1 || candidate.Installers[0] != "nvm" {
			t.Errorf("installers = %v", candidate.Installers)
		}
	}
}
