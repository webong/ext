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
