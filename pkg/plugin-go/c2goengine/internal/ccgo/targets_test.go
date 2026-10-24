package ccgo

import (
	"go/build/constraint"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var generatedName = regexp.MustCompile(`^generated_([a-z0-9]+)_([a-z0-9]+)\.go$`)

// buildLine returns the constraint of a file: its //go:build line.
func buildLine(t *testing.T, path string) constraint.Expr {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range regexp.MustCompile(`(?m)^//go:build .*$`).FindAllString(string(data[:min(len(data), 2000)]), 1) {
		expr, err := constraint.Parse(line)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return expr
	}
	t.Fatalf("%s: no //go:build line", path)
	return nil
}

func holds(expr constraint.Expr, goos, goarch string) bool {
	return expr.Eval(func(tag string) bool { return tag == goos || tag == goarch })
}

// TestTargetsAgree keeps three lists in step: the platforms that have a generated file, the
// platforms call.go builds for, and the ones call_unsupported.go covers. A platform with a file but
// no call.go would build without the call; one with call.go and no file would not build at all.
func TestTargetsAgree(t *testing.T) {
	files, err := filepath.Glob("generated_*.go")
	if err != nil {
		t.Fatal(err)
	}
	type target struct{ goos, goarch string }
	generated := map[target]bool{}
	for _, f := range files {
		m := generatedName.FindStringSubmatch(f)
		if m == nil {
			t.Errorf("%s does not match generated_<goos>_<goarch>.go", f)
			continue
		}
		tg := target{m[1], m[2]}
		generated[tg] = true
		// The file's own constraint is the one its name implies.
		if !holds(buildLine(t, f), tg.goos, tg.goarch) || holds(buildLine(t, f), "other", tg.goarch) {
			t.Errorf("%s: its //go:build line does not select exactly %s/%s", f, tg.goos, tg.goarch)
		}
	}
	call, unsupported := buildLine(t, "call.go"), buildLine(t, "call_unsupported.go")
	for _, goos := range []string{"linux", "darwin", "windows", "freebsd", "openbsd", "netbsd", "android", "ios", "js", "wasip1"} {
		for _, goarch := range []string{"amd64", "arm64", "386", "arm", "riscv64", "ppc64le", "s390x", "loong64", "wasm"} {
			tg := target{goos, goarch}
			if has, builds := generated[tg], holds(call, goos, goarch); has != builds {
				t.Errorf("%s/%s: generated file %v, call.go builds %v", goos, goarch, has, builds)
			}
			if holds(call, goos, goarch) == holds(unsupported, goos, goarch) {
				t.Errorf("%s/%s: call.go and call_unsupported.go must cover each platform exactly once", goos, goarch)
			}
		}
	}
	if len(generated) == 0 {
		t.Error("no generated file")
	}
}

// TestTranslationsMatchInputs requires every platform's translation to have been made from the
// inputs digest the module records in inputs.sha256: regenerating on one platform after the C
// changed, and not on the others, leaves the others naming an older digest. (Whether the C has
// changed since is for the generator to say: it writes a new digest, which the workflow compares.)
func TestTranslationsMatchInputs(t *testing.T) {
	want, err := os.ReadFile("inputs.sha256")
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.TrimSpace(string(want))
	files, err := filepath.Glob("generated_*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no generated files (%v)", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.SplitN(string(data[:min(len(data), 1<<16)]), "\n", 3)
		if len(lines) < 3 || lines[1] != "// c2goengine inputs sha256:"+digest {
			t.Errorf("%s was not made from inputs.sha256 (%s): regenerate it with tools", f, digest[:12])
		}
	}
}
