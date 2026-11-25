package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

func TestLegacyAdapterPlan(t *testing.T)     { testAdapterDependencyPlan(t, false) }
func TestAdapterDependencyPlan(t *testing.T) { testAdapterDependencyPlan(t, true) }

func testAdapterDependencyPlan(t *testing.T, modern bool) {
	lib := os.Getenv("EXT_ACCEPTANCE_LIBRARY")
	if lib == "" {
		t.Skip("required by scripts/package-acceptance.sh")
	}
	home, bin, outside := t.TempDir(), t.TempDir(), t.TempDir()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	ext := filepath.Join(bin, "ext"+suffix)
	goBuild(t, ext, ".")
	stageAcceptanceEngine(t, bin, lib)
	executable := filepath.Join(bin, "evaluator"+suffix)
	goBuild(t, executable, "./testdata/evaluator")
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	source := func(name, spaces, dependencies string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "evaluator"+suffix), data, 0700); err != nil {
			t.Fatal(err)
		}
		if spaces == "storage,extra" {
			build := exec.Command("go", "build", "-ldflags=-buildid=adapter-upgrade-v2", "-o", filepath.Join(dir, "evaluator"+suffix), "./testdata/evaluator")
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build upgrade: %v %s", err, out)
			}
		}
		text := fmt.Sprintf("api_version = %q\nname = %q\nruntime = %q\nsurfaces = %q\nexecutable = %q\ncapabilities = %q\nself_contained = %q\n", adapter.APIVersion, name, "computer", "shell", "evaluator"+suffix, "list,observe,validate,run,run-verified,doctor,share", "true")
		text += fmt.Sprintf("share_spaces = %q\ndependencies_%s = %q\n", spaces, runtime.GOOS, dependencies)
		if err := adapter.WriteKeyValueManifest(dir, text); err != nil {
			t.Fatal(err)
		}
		if modern && dependencies != "" {
			path := filepath.Join(dir, "ext.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var pm packagekit.Manifest
			if err := json.Unmarshal(raw, &pm); err != nil {
				t.Fatal(err)
			}
			var meta map[string]any
			if err := json.Unmarshal(pm.Metadata[adapter.MetadataKey], &meta); err != nil {
				t.Fatal(err)
			}
			space, rest, _ := strings.Cut(dependencies, ":")
			provider, version, _ := strings.Cut(rest, "@")
			delete(meta, "dependencies_"+runtime.GOOS)
			meta["share_dependencies"] = []string{space + ":" + provider}
			pm.Metadata[adapter.MetadataKey], err = json.Marshal(meta)
			if err != nil {
				t.Fatal(err)
			}
			pm.Requires = []packagekit.Dependency{{Provider: provider, Contract: plugin.ContractRef{Name: plugin.AdapterContractName, Version: version}, Operation: "share", OS: runtime.GOOS, Arch: runtime.GOARCH}}
			raw, err = json.MarshalIndent(pm, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := adapter.Seal(dir, adapter.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	run := func(want int, contains string, args ...string) string {
		t.Helper()
		cmd := exec.Command(ext, args...)
		cmd.Dir = outside
		cmd.Env = acceptanceEnv(home)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		code := 0
		if err != nil {
			e, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = e.ExitCode()
		}
		if code != want || !strings.Contains(out.String(), contains) {
			t.Fatalf("%v: code %d %s; want %d %q", args, code, out.String(), want, contains)
		}
		return out.String()
	}
	consumer := source("dependent", "dependent", "storage:provider@2.0")
	provider := source("provider", "storage", "")
	run(0, "installed", "package", "install", consumer)
	run(1, "requires storage:provider@2.0", "package", "plan")
	run(1, "requires storage:provider@2.0", "package", "trust", "dependent")
	alternative := source("alternative", "storage", "")
	run(0, "installed", "package", "install", alternative)
	run(1, "requires storage:provider@2.0", "package", "plan")
	run(0, "installed", "package", "install", provider)
	out := run(0, `"status":"ok"`, "package", "plan")
	if !strings.Contains(out, `"operation":"share"`) {
		t.Fatalf("missing projected edge: %s", out)
	}
	run(0, "trusted", "package", "trust", "provider")
	run(0, "trusted", "package", "trust", "dependent")
	run(1, "requires storage:provider@2.0", "package", "remove", "provider")
	incompatible := source("provider", "different", "")
	run(1, "does not satisfy", "package", "upgrade", incompatible)
	run(0, "trusted=true", "package", "verify", "provider")
	cycle := source("provider", "storage", "dependent:dependent@2.0")
	run(1, "unresolved dependencies (invalid)", "package", "upgrade", cycle)
	compatible := source("provider", "storage,extra", "")
	run(0, "installed", "package", "upgrade", compatible)
	run(0, `"status":"ok"`, "package", "plan")
	run(0, "trusted=false", "package", "verify", "provider")
	run(0, "removed", "package", "remove", "provider", "--force")
	run(1, "requires storage:provider@2.0", "package", "plan")
	// Bundle plans must project legacy edges before ordering. Publish the
	// provider before the alphabetically earlier dependent, and recheck the
	// installed dependents when a bundle replaces only the provider.
	files := t.TempDir()
	archive := func(dir, name string) map[string]any {
		t.Helper()
		manifest, err := os.ReadFile(filepath.Join(dir, "ext.json"))
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		writer := zip.NewWriter(&buf)
		for _, path := range []string{"ext.json", "evaluator" + suffix} {
			contents, err := os.ReadFile(filepath.Join(dir, path))
			if err != nil {
				t.Fatal(err)
			}
			header := &zip.FileHeader{Name: path, Method: zip.Deflate}
			header.SetMode(0700)
			entry, err := writer.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := entry.Write(contents); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(files, name), buf.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		return map[string]any{"manifest": json.RawMessage(manifest), "archive": map[string]string{"source": name, "sha256": fmt.Sprintf("%x", sha256.Sum256(buf.Bytes()))}}
	}
	dependentItem := archive(consumer, "dependent.zip")
	providerItem := archive(compatible, "provider.zip")
	bundleFile := filepath.Join(files, "bundle.json")
	writeBundle := func(items ...map[string]any) {
		t.Helper()
		data, err := json.Marshal(map[string]any{"apiVersion": "ext.bundle/v1", "target": runtime.GOOS + "/" + runtime.GOARCH, "packages": items})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bundleFile, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeBundle(dependentItem)
	missing := "requires storage:provider@2.0"
	if modern {
		missing = "not_found"
	}
	run(1, missing, "bundle", "plan", bundleFile)
	writeBundle(dependentItem, providerItem)
	planned := run(0, `"operation":"share"`, "bundle", "plan", bundleFile)
	var report struct {
		Plan struct{ Order []struct{ ID string } }
	}
	if err := json.Unmarshal([]byte(planned), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Plan.Order) != 2 || report.Plan.Order[0].ID != "provider" || report.Plan.Order[1].ID != "dependent" {
		t.Fatalf("bundle dependency order: %s", planned)
	}
	run(0, "complete", "bundle", "install", bundleFile)
	run(0, `"status":"ok"`, "package", "plan")
	run(0, "trusted", "package", "trust", "provider")
	badProvider := archive(incompatible, "bad-provider.zip")
	writeBundle(badProvider)
	// This standalone bundle resolves by itself, but breaks an installed
	// adapter's sharing requirement. Nothing may publish from that preview.
	run(1, "does not satisfy", "bundle", "install", bundleFile)
	run(0, "trusted=true", "package", "verify", "provider")
	run(0, `"status":"ok"`, "package", "plan")

}
