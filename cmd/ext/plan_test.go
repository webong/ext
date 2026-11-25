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

	"github.com/webong/ext/cmd/ext/internal/pkgcmd"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

// Plan fixtures are sealed data packages: nothing is executed to discover
// requirements or preview an upgrade/removal.
func TestPackagePlan(t *testing.T) {
	libraryPath := os.Getenv("EXT_ACCEPTANCE_LIBRARY")
	if libraryPath == "" {
		t.Skip("run scripts/package-acceptance.sh for C planner acceptance")
	}
	home, bin, outside := t.TempDir(), t.TempDir(), t.TempDir()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	ext := filepath.Join(bin, "ext"+suffix)
	goBuild(t, ext, ".")
	stageAcceptanceEngine(t, bin, libraryPath)
	run := func(want int, contains string, args ...string) string {
		t.Helper()
		cmd := exec.Command(ext, args...)
		cmd.Dir = outside
		cmd.Env = acceptanceEnv(home)
		output, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				code = e.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if code != want || !strings.Contains(string(output), contains) {
			t.Fatalf("%v: code=%d output=%s; want %d %q", args, code, output, want, contains)
		}
		if len(args) >= 2 && args[0] == "package" && args[1] == "plan" && strings.HasPrefix(contains, `"status":`) {
			var report struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(output, &report); err != nil {
				t.Fatal(err)
			}
			if `"status":"`+report.Status+`"` != contains {
				t.Fatalf("top-level plan status %q, want %s; output %s", report.Status, contains, output)
			}
		}
		return string(output)
	}
	ref := plugin.ContractRef{Name: "example.values", Version: "v1"}
	source := func(id, revision string, provides bool, requires []packagekit.Dependency) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "data"), []byte(revision), 0600); err != nil {
			t.Fatal(err)
		}
		contracts := []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.consumer", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}
		if provides {
			contracts = []plugin.Contract{{ContractRef: ref, Operations: []plugin.Operation{{Name: "read"}}}}
		}
		m, err := packagekit.SourceManifest(id, "1.0.0", contracts, dir, []string{"data"})
		if err != nil {
			t.Fatal(err)
		}
		m.Requires = requires
		if err := store.WriteManifest(dir, m); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	requirement := []packagekit.Dependency{{Contract: ref, Operation: "read"}}
	consumer := source("example/consumer", "consumer-v1", false, requirement)
	run(0, "installed", "package", "install", consumer)
	run(1, `"status":"not_found"`, "package", "plan")
	run(1, "unresolved package dependencies", "package", "trust", "example/consumer")
	provider := source("example/provider", "provider-v1", true, nil)
	run(0, `"status":"ok"`, "package", "plan", "--with", provider)
	run(1, `"status":"not_found"`, "package", "plan") // preview did not install
	run(0, "installed", "package", "install", provider)
	report := run(0, `"status":"ok"`, "package", "plan")
	var plan struct {
		Plan       packagekit.Plan   `json:"plan"`
		Unapproved []plugin.Identity `json:"unapproved"`
	}
	if err := json.Unmarshal([]byte(report), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Plan.Bindings) != 1 || plan.Plan.Bindings[0].Provider.ID != "example/provider" || len(plan.Unapproved) != 2 {
		t.Fatalf("plan: %+v", plan)
	}
	if len(plan.Plan.Order) != 2 || plan.Plan.Order[0].ID != "example/provider" {
		t.Fatalf("not provider-first: %+v", plan.Plan.Order)
	}

	commandDir := t.TempDir()
	guest := filepath.Join(commandDir, "hello"+suffix)
	goBuild(t, guest, "./testdata/guest")
	cm, err := packagekit.ExecutableManifest("example/hello", "1.0.0", []plugin.Contract{pkgcmd.RunContract}, guest)
	if err != nil {
		t.Fatal(err)
	}
	cm.Descriptor.Identity.Revision = "child"
	cm.Metadata = map[string]json.RawMessage{pkgcmd.MetadataKey: json.RawMessage(`{"commands":[{"name":"hi"}]}`)}
	// hello -> consumer -> provider exercises transitive admission. Only the
	// command is launched; the data packages describe the dependency chain.
	cm.Requires = []packagekit.Dependency{{Contract: plugin.ContractRef{Name: "example.consumer", Version: "v1"}, Operation: "read"}}
	if err := store.WriteManifest(commandDir, cm); err != nil {
		t.Fatal(err)
	}
	run(0, "installed", "package", "install", commandDir)
	run(0, "trusted", "package", "trust", "example/hello")
	run(1, "required package example/consumer is not approved", "hi", "blocked-middle")
	run(0, "trusted", "package", "trust", "example/consumer")
	chain := run(0, `"status":"ok"`, "package", "plan")
	if err := json.Unmarshal([]byte(chain), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Plan.Order) != 3 || plan.Plan.Order[0].ID != "example/provider" || plan.Plan.Order[1].ID != "example/consumer" || plan.Plan.Order[2].ID != "example/hello" || len(plan.Plan.Bindings) != 2 {
		t.Fatalf("incorrect transitive plan: %+v", plan.Plan)
	}
	run(1, "required package example/provider is not approved", "hi", "blocked")
	run(0, "trusted", "package", "trust", "example/provider")
	run(0, "trusted", "package", "trust", "example/consumer")
	run(0, "dependency-ready", "hi", "dependency-ready")
	run(0, "no longer trusted", "package", "untrust", "example/provider")
	run(1, "required package example/provider is not approved", "hi", "blocked-after-revoke")
	run(0, "trusted", "package", "trust", "example/provider")
	// A valid provider replacement preserves resolution but invalidates approval.
	nextProvider := source("example/provider", "provider-next", true, nil)
	run(0, "installed", "package", "upgrade", nextProvider)
	run(0, `"status":"ok"`, "package", "plan")
	run(1, "required package example/provider is not approved", "hi", "blocked-after-upgrade")
	run(0, "trusted", "package", "trust", "example/provider")
	run(0, "chain-ready", "hi", "chain-ready")
	run(1, "removal leaves unresolved dependencies", "package", "remove", "example/consumer")
	run(0, "no longer trusted", "package", "untrust", "example/consumer")
	run(1, "required package example/consumer is not approved", "hi", "blocked-middle-revoked")
	run(0, "trusted", "package", "trust", "example/consumer")
	run(0, "removed", "package", "remove", "example/hello")

	run(1, `"status":"not_found"`, "package", "plan", "--without", "example/provider")
	run(1, "removal leaves unresolved dependencies", "package", "remove", "example/provider")
	// A replacement that stops providing the contract is visible before upgrade.
	replacement := source("example/provider", "provider-v2", false, nil)
	run(1, `"status":"not_found"`, "package", "plan", "--with", replacement)
	run(1, "package change leaves unresolved dependencies", "package", "upgrade", replacement)
	// The archive path also performs the preflight before replacing content.
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	for _, name := range []string{"data", store.ManifestName} {
		data, err := os.ReadFile(filepath.Join(replacement, name))
		if err != nil {
			t.Fatal(err)
		}
		entry, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "replacement.zip")
	if err := os.WriteFile(archivePath, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	pin := fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
	run(1, "package change leaves unresolved dependencies", "package", "upgrade", archivePath, "--sha256", pin)
	run(1, "package change leaves unresolved dependencies", "package", "install", archivePath, "--sha256", pin)
	run(0, "trusted=true", "package", "verify", "example/provider")
	run(0, `"status":"ok"`, "package", "plan")
	alternative := source("example/alternative", "alternative-v1", true, nil)
	run(0, "installed", "package", "install", alternative)
	run(1, `"status":"ambiguous"`, "package", "plan")
	run(1, "unresolved package dependencies", "package", "trust", "example/alternative")
	run(0, "removed", "package", "remove", "example/alternative")
	run(0, "removed", "package", "remove", "example/provider", "--force")
	run(1, `"status":"not_found"`, "package", "plan")
	run(0, "removed", "package", "remove", "example/consumer")
	run(0, `"status":"ok"`, "package", "plan")

	// A persisted named selector chooses the intended provider among equivalent
	// contracts, without freezing its revision or granting upgrade approval.
	named := source("example/named", "named-v1", false, []packagekit.Dependency{
		{Contract: ref, Operation: "read", Provider: "example/provider", OS: runtime.GOOS, Arch: runtime.GOARCH},
		{Contract: ref, Operation: "read", Provider: "example/other-platform", OS: "otheros"},
	})
	run(0, "installed", "package", "install", named)
	run(0, "installed", "package", "install", alternative)
	run(1, `"status":"not_found"`, "package", "plan")
	run(0, "installed", "package", "install", provider)
	checkNamed := func() {
		t.Helper()
		report := run(0, `"status":"ok"`, "package", "plan")
		if err := json.Unmarshal([]byte(report), &plan); err != nil {
			t.Fatal(err)
		}
		if len(plan.Plan.Bindings) != 1 || plan.Plan.Bindings[0].Requirement.Provider != "example/provider" || plan.Plan.Bindings[0].Requirement.Identity != nil || plan.Plan.Bindings[0].Provider.ID != "example/provider" {
			t.Fatalf("named provider binding: %+v", plan.Plan.Bindings)
		}
	}
	checkNamed()
	run(0, "trusted", "package", "trust", "example/provider")
	run(0, "installed", "package", "upgrade", nextProvider)
	checkNamed()
	run(0, "trusted=false", "package", "verify", "example/provider")
	run(1, `"status":"not_found"`, "package", "plan", "--without", "example/provider")
	run(1, "removal leaves unresolved dependencies", "package", "remove", "example/provider")
}
