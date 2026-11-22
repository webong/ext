package program

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestMatchDeclared(t *testing.T) {
	root := t.TempDir()
	firstBin := filepath.Join(root, "first")
	secondBin := filepath.Join(root, "second")
	for _, dir := range []string{firstBin, secondBin} {
		writeProgram(t, filepath.Join(dir, "helper"))
	}
	writeProgram(t, filepath.Join(secondBin, "only-second"))
	t.Chdir(root)
	t.Setenv("PATH", secondBin)
	command, directory := "helper", "directory"
	if runtime.GOOS == "windows" {
		t.Setenv("PATHEXT", ".EXE")
		command += ".exe"
		directory += ".exe"
	}
	app := filepath.Join(root, "Example.app")
	dirOnPath := filepath.Join(firstBin, directory)
	for _, dir := range []string{app, dirOnPath} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	receipt := filepath.Join(root, "example.receipt")
	if err := os.WriteFile(receipt, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	search := strings.Join([]string{firstBin, firstBin, secondBin}, string(os.PathListSeparator))

	for _, tt := range []struct {
		name      string
		locations []string
		pathList  string
		want      string
	}{
		{"declared directory takes precedence", []string{app, "helper"}, search, app},
		{"literal file needs no execute permission", []string{receipt}, "", receipt},
		{"command takes precedence", []string{"helper", app}, search, filepath.Join(firstBin, command)},
		{"duplicates and blanks", []string{"missing", " missing ", " ", " helper ", "helper", app}, search, filepath.Join(firstBin, command)},
		{"missing declared path falls through", []string{filepath.Join(root, "missing"), app}, search, app},
		{"directory is not a command", []string{directory}, search, ""},
		{"unlisted command and process PATH are ignored", []string{"missing", "only-second"}, firstBin, ""},
		{"relative declarations are ignored", []string{"./example.receipt", filepath.Join("first", command)}, search, ""},
		{"relative search directories are ignored", []string{"helper"}, "first", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MatchDeclared(t.Context(), tt.locations, tt.pathList)
			if got != tt.want || err != nil {
				t.Fatalf("MatchDeclared = %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}

func TestMatchDeclaredBounds(t *testing.T) {
	existing := t.TempDir()
	dirs := strings.Join(slices.Repeat([]string{"."}, maxDeclaredSearchDirs), string(os.PathListSeparator))
	locations := slices.Repeat([]string{existing}, maxDeclaredLocations)
	if got, err := MatchDeclared(t.Context(), locations, dirs); got != existing || err != nil {
		t.Fatalf("MatchDeclared at count limits = %q, %v, want %q", got, err, existing)
	}

	for _, tt := range []struct {
		name      string
		locations []string
		pathList  string
	}{
		{"too many declarations", append(slices.Clone(locations), existing), dirs},
		{"too many search entries", locations, dirs + string(os.PathListSeparator) + "."},
		{"too many declaration bytes", []string{existing, strings.Repeat("a", maxDeclaredBytes)}, ""},
		{"too many search bytes", locations, strings.Repeat("a", maxDeclaredBytes+1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MatchDeclared(t.Context(), tt.locations, tt.pathList)
			if got != "" || !errors.Is(err, ErrDiscoveryLimit) {
				t.Fatalf("MatchDeclared = %q, %v, want ErrDiscoveryLimit", got, err)
			}
		})
	}
}

func TestMatchDeclaredCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err := MatchDeclared(ctx, []string{t.TempDir()}, "")
	if got != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("MatchDeclared = %q, %v, want context.Canceled", got, err)
	}
}
