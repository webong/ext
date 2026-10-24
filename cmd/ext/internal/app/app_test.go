package app

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/webong/ext/cmd/ext/internal/registry"
)

func testRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	r := registry.New()
	echo := func(args []string, stdout, stderr io.Writer) int {
		io.WriteString(stdout, strings.Join(args, " ")+"\n")
		return 7
	}
	for _, c := range []registry.Command{
		{Name: "ctx", Source: registry.Builtin, Version: "1.0", Run: echo},
		{Name: "ctn", Source: registry.Package, Version: "2.0", Run: echo},
	} {
		if err := r.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func exec(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(testRegistry(t), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRunsTheNamedCommandWithTheRestOfTheArguments(t *testing.T) {
	code, out, _ := exec(t, "ctx", "--flag", "x")
	if code != 7 || out != "--flag x\n" {
		t.Fatalf("code %d out %q", code, out)
	}
}

func TestCommandsAndWhichReportHowEachWasLoaded(t *testing.T) {
	_, out, _ := exec(t, "commands")
	for _, want := range []string{"ctx", "builtin", "1.0", "ctn", "package", "2.0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("commands output lacks %q:\n%s", want, out)
		}
	}
	code, out, _ := exec(t, "which", "ctn")
	if code != 0 || out != "ctn: package 2.0\n" {
		t.Fatalf("which = %d %q", code, out)
	}
}

func TestUnknownCommandSuggestsTheClosestName(t *testing.T) {
	code, _, errOut := exec(t, "cxt")
	if code != 2 || !strings.Contains(errOut, `did you mean "ctx"`) {
		t.Fatalf("code %d stderr %q", code, errOut)
	}
	if code, _, errOut = exec(t, "which", "nothing-like-it"); code != 2 || !strings.Contains(errOut, "ext commands") {
		t.Fatalf("code %d stderr %q", code, errOut)
	}
}

func TestVersionListsEmbeddedVersions(t *testing.T) {
	_, out, _ := exec(t, "version")
	if !strings.HasPrefix(out, "ext "+Version+"\n") || !strings.Contains(out, "ctx 1.0 (builtin)") {
		t.Fatalf("version output:\n%s", out)
	}
}

func TestNoArgumentsPrintsUsageAndFails(t *testing.T) {
	code, out, errOut := exec(t)
	if code != 2 || out != "" || !strings.Contains(errOut, "usage: ext") {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
}
