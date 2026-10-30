package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webong/ext/res/system/program"
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
