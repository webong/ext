package app

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/res/system/service/program"
)

func TestInstallerCommandAddRemoveLs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("EXT_HOME", home)
	run := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := programCommand(nil, append([]string{"installer"}, args...), &out, &errOut)
		return code, out.String(), errOut.String()
	}
	if code, out, errOut := run("add", "mytools", "~/tools/*/bin", "${MYTOOLS_HOME:-~/mytools}/v/*/bin"); code != 0 {
		t.Fatalf("add = %d %q %q", code, out, errOut)
	}
	// Adding again extends the installer and does not repeat a pattern. A path with
	// no drive letter is not absolute on Windows.
	opt := "/opt/mytools/*/bin"
	if runtime.GOOS == "windows" {
		opt = "C:" + opt
	}
	if code, _, errOut := run("add", "mytools", "~/tools/*/bin", opt); code != 0 {
		t.Fatalf("extend = %d %q", code, errOut)
	}
	got := readConfiguredInstallers()
	want := []configuredInstaller{{Name: "mytools", Roots: []string{"~/tools/*/bin", "${MYTOOLS_HOME:-~/mytools}/v/*/bin", opt}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("installers = %#v, want %#v", got, want)
	}
	if code, out, _ := run("ls"); code != 0 || !strings.HasPrefix(out, "mytools\t~/tools/*/bin\t") {
		t.Fatalf("ls = %d %q", code, out)
	}
	for _, bad := range [][]string{
		{"add", "not a name", "/x"}, {"add", "x", "relative/bin"}, {"add", "x", "bin"}, {"add", "x"}, {"add"},
		{"remove"}, {"remove", "missing"}, {"frobnicate"}, {},
	} {
		if code, _, _ := run(bad...); code == 0 {
			t.Fatalf("%v must fail", bad)
		}
	}
	if code, _, _ := run("remove", "mytools"); code != 0 {
		t.Fatal("remove failed")
	}
	if got := readConfiguredInstallers(); len(got) != 0 {
		t.Fatalf("after remove: %#v", got)
	}
}

func TestInstallerFileMustBeValid(t *testing.T) {
	home := t.TempDir()
	t.Setenv("EXT_HOME", home)
	for name, body := range map[string]string{
		"unknown field": `{"version":1,"installers":[],"extra":1}`,
		"bad version":   `{"version":2,"installers":[]}`,
		"bad name":      `{"version":1,"installers":[{"name":"a b","roots":["/x"]}]}`,
		"no roots":      `{"version":1,"installers":[{"name":"a","roots":[]}]}`,
		"relative root": `{"version":1,"installers":[{"name":"a","roots":["bin"]}]}`,
		"duplicate":     `{"version":1,"installers":[{"name":"a","roots":["/x"]},{"name":"a","roots":["/y"]}]}`,
	} {
		if err := os.WriteFile(filepath.Join(home, "installers.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadInstallerFile(); err == nil {
			t.Errorf("%s: want an error", name)
		}
		// Discovery never fails because of the file: it declares no installers.
		if got := readConfiguredInstallers(); got != nil {
			t.Errorf("%s: discovery read %#v", name, got)
		}
	}
}

func TestConfiguredInstallersJoinThePathScan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses executable bits")
	}
	root := t.TempDir()
	for _, version := range []string{"v1", "v2"} {
		dir := filepath.Join(root, version, "bin")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "node"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	installers := []configuredInstaller{{Name: "mytools", Roots: []string{"${MYTOOLS}/*/bin"}}}
	env := func(name string) string {
		if name == "MYTOOLS" {
			return root
		}
		return ""
	}
	reports := configuredInstallerReports(installers, []string{"node"}, env)
	if len(reports["mytools"]) != 2 {
		t.Fatalf("reports = %#v", reports)
	}
	// One of the two is also on PATH: it becomes one candidate that names the installer.
	onPath := filepath.Join(root, "v2", "bin")
	merged := program.Merge(program.Scan([]string{"node"}, onPath), reports)
	if len(merged) != 2 {
		t.Fatalf("merged = %#v", merged)
	}
	for _, candidate := range merged {
		if !reflect.DeepEqual(candidate.Installers, []string{"mytools"}) {
			t.Errorf("installers = %v", candidate.Installers)
		}
		if candidate.OnPath != strings.HasPrefix(candidate.Path, onPath) {
			t.Errorf("on_path of %s = %v", candidate.Path, candidate.OnPath)
		}
	}
}
