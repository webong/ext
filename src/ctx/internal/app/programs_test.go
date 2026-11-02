package app

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/webong/ext/res/system/service/program"
)

func TestProgramsEnvironmentValueIsOneTabSeparatedLinePerCandidate(t *testing.T) {
	value := programsEnvironmentValue([]program.Candidate{
		{Name: "php", Path: "/p/php", RealPath: "/r/php", Version: "8.3.1", Installers: []string{"asdf", "homebrew"}, OnPath: true},
		{Name: "php", Path: "/q/php", RealPath: "/q/php"},
		{Name: "php", Path: "/bad\tpath/php", RealPath: "/bad/php"},
	})
	want := "php\t/p/php\t/r/php\t8.3.1\tasdf,homebrew\t1\nphp\t/q/php\t/q/php\t\t\t0"
	if value != want {
		t.Fatalf("value = %q, want %q (a candidate with a control character is dropped)", value, want)
	}
	if programsEnvironmentValue(nil) != "" {
		t.Fatal("no candidates must give an empty value")
	}
}

func TestPathWithoutShimsDropsTheDirectoryCTXLivesIn(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	other := t.TempDir()
	t.Setenv("PATH", strings.Join([]string{filepath.Dir(self), other}, string(os.PathListSeparator)))
	if got := pathWithoutShims(); got != other {
		t.Fatalf("pathWithoutShims = %q, want only %q", got, other)
	}
}

func TestUniqueSorted(t *testing.T) {
	got := uniqueSorted([]string{"node", "php", "", "node"})
	if strings.Join(got, ",") != "node,php" {
		t.Fatalf("uniqueSorted = %v", got)
	}
}

func TestApplyRecognisedAddsInstallersByRealFile(t *testing.T) {
	candidates := []program.Candidate{
		{Name: "node", Path: "/p/node", RealPath: "/r/node", OnPath: true, Source: program.SourcePath},
		{Name: "node", Path: "/q/node", RealPath: "/q/node", Installers: []string{"homebrew"}, Source: program.SourceInstaller},
	}
	got := applyRecognised(candidates, []recognition{{RealPath: "/r/node", Installer: "nvm"}, {RealPath: "/q/node", Installer: "asdf"}, {RealPath: "/q/node", Installer: "homebrew"}, {RealPath: "/gone/node", Installer: "x"}})
	if !reflect.DeepEqual(got[0].Installers, []string{"nvm"}) || got[0].Source != program.SourceInstaller || !got[0].OnPath {
		t.Fatalf("first = %#v", got[0])
	}
	if !reflect.DeepEqual(got[1].Installers, []string{"asdf", "homebrew"}) {
		t.Fatalf("second = %#v, want installers merged without repeats", got[1])
	}
	if candidates[0].Installers != nil {
		t.Fatal("applyRecognised changed its input")
	}
}

func TestProgramCommandRecogniseAndForget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("EXT_HOME", home)
	t.Setenv("HOME", home)
	file := filepath.Join(home, "node")
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := programCommand(nil, []string{"recognise", file, "nvm"}, &out, &errOut); code != 0 {
		t.Fatalf("recognise = %d: %s", code, errOut.String())
	}
	if got := readRecognised(); len(got) != 1 || got[0].RealPath != real || got[0].Installer != "nvm" {
		t.Fatalf("recognised = %#v", got)
	}
	out.Reset()
	if code := programCommand(nil, []string{"recognise", file, "nvm"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "already") {
		t.Fatalf("repeat = %d %q", code, out.String())
	}
	for _, bad := range [][]string{{"recognise", file, "not a name"}, {"recognise", filepath.Join(home, "missing"), "nvm"}, {"recognise"}, {"unknown", file}} {
		errOut.Reset()
		if code := programCommand(nil, bad, &out, &errOut); code == 0 {
			t.Fatalf("%v must fail", bad)
		}
	}
	if code := programCommand(nil, []string{"forget", file, "asdf"}, &out, &errOut); code == 0 {
		t.Fatal("forgetting what was never recognised must fail")
	}
	if code := programCommand(nil, []string{"forget", file}, &out, &errOut); code != 0 {
		t.Fatalf("forget = %d", code)
	}
	if got := readRecognised(); len(got) != 0 {
		t.Fatalf("recognised after forget = %#v", got)
	}
}
