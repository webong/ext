package pkgcmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/webong/ext/cmd/ext/internal/nativeplugin"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

type planIssue struct {
	Consumer    plugin.Identity       `json:"consumer"`
	Requirement packagekit.Dependency `json:"requirement"`
	Status      string                `json:"status"`
}
type packagePlan struct {
	Status     string            `json:"status"`
	Plan       packagekit.Plan   `json:"plan"`
	Issues     []planIssue       `json:"issues"`
	Unapproved []plugin.Identity `json:"unapproved"`
	// Previous bindings make the effect of removing or replacing a provider visible.
	Previous *packagekit.Plan `json:"previous,omitempty"`
}

func code(err error) string {
	if err == nil {
		return "ok"
	}
	var e *library.CodeError
	if errors.As(err, &e) {
		return e.Name
	}
	return "error"
}
func (m Manager) plan(args []string, out, stderr io.Writer) int {
	var without, with string
	if len(args) != 0 {
		if len(args) != 2 || (args[0] != "--without" && args[0] != "--with") {
			fmt.Fprintln(stderr, "usage: ext package plan [--without <id> | --with <sealed-package-dir>]")
			return 2
		}
		if args[0] == "--without" {
			without = args[1]
		} else {
			with = args[1]
		}
	}
	report, err := m.resolvePlan(without, with)
	if err != nil {
		fmt.Fprintln(stderr, "ext: package plan:", err)
		return 1
	}
	if err := json.NewEncoder(out).Encode(report); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if report.Status != "ok" {
		return 1
	}
	return 0
}
func (m Manager) resolvePlan(without, with string) (packagePlan, error) {
	report := packagePlan{Issues: []planIssue{}, Unapproved: []plugin.Identity{}}
	lib, err := nativeplugin.Open()
	if err != nil {
		return report, err
	}
	native, err := lib.Open("store", map[string]any{"root": m.Store.Root()}, nil)
	if err != nil {
		return report, err
	}
	defer native.Close()
	manifests := []packagekit.Manifest{}
	var rows []struct {
		Manifest packagekit.Manifest `json:"manifest"`
	}
	if err := native.Call("list", nil, &rows); err != nil {
		return report, err
	}
	for _, row := range rows {
		manifests = append(manifests, row.Manifest)
	}
	if without != "" || with != "" {
		previous := packagekit.Plan{}
		if projected, err := adapter.ProjectDependencies(manifests, runtime.GOOS); err == nil {
			if err := lib.Library().Call("package.resolve_for", map[string]any{"packages": projected, "os": runtime.GOOS, "arch": runtime.GOARCH}, &previous); err == nil {
				report.Previous = &previous
			}
		}
	}
	candidateID := ""
	if with != "" {
		path := filepath.Join(with, store.ManifestName)
		info, err := os.Lstat(path)
		if err != nil {
			return report, err
		}
		if !info.Mode().IsRegular() || info.Size() > 4<<20 {
			return report, fmt.Errorf("candidate manifest is not a bounded regular file")
		}
		f, err := os.Open(path)
		if err != nil {
			return report, err
		}
		data, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
		closeErr := f.Close()
		if err != nil {
			return report, err
		}
		if closeErr != nil {
			return report, closeErr
		}
		if len(data) > 4<<20 {
			return report, fmt.Errorf("candidate manifest exceeds limit")
		}
		if err := lib.Library().Call("package_verify", map[string]any{"manifest": json.RawMessage(data), "root": with}, nil); err != nil {
			return report, err
		}
		var candidate packagekit.Manifest
		if err := json.Unmarshal(data, &candidate); err != nil {
			return report, err
		}
		candidateID = candidate.Descriptor.Identity.ID
		next := make([]packagekit.Manifest, 0, len(manifests)+1)
		for _, manifest := range manifests {
			if manifest.Descriptor.Identity.ID != candidateID {
				next = append(next, manifest)
			}
		}
		manifests = append(next, candidate)
	}
	if without != "" {
		found := false
		next := make([]packagekit.Manifest, 0, len(manifests))
		for _, manifest := range manifests {
			if manifest.Descriptor.Identity.ID == without {
				found = true
			} else {
				next = append(next, manifest)
			}
		}
		if !found {
			return report, fmt.Errorf("package %s not found", without)
		}
		manifests = next
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].Descriptor.Identity.ID < manifests[j].Descriptor.Identity.ID })
	for _, manifest := range manifests {
		if err := lib.Library().Call("package.validate", manifest, nil); err != nil {
			return report, fmt.Errorf("package %s: %w", manifest.Descriptor.Identity.ID, err)
		}
	}
	manifests, err = adapter.ProjectDependencies(manifests, runtime.GOOS)
	if err != nil {
		return report, err
	}
	resolveErr := lib.Library().Call("package.resolve_for", map[string]any{"packages": manifests, "os": runtime.GOOS, "arch": runtime.GOARCH}, &report.Plan)
	report.Status = code(resolveErr)
	if resolveErr != nil && report.Status == "error" {
		return report, resolveErr
	}
	descriptors := make([]plugin.Descriptor, 0, len(manifests))
	for _, manifest := range manifests {
		descriptors = append(descriptors, manifest.Descriptor)
	}
	for _, manifest := range manifests {
		for _, requirement := range manifest.Requires {
			err := lib.Library().Call("descriptor.select", map[string]any{"candidates": descriptors, "requirement": requirement}, nil)
			if err != nil {
				if code(err) == "error" {
					return report, err
				}
				report.Issues = append(report.Issues, planIssue{Consumer: manifest.Descriptor.Identity, Requirement: requirement, Status: code(err)})
			}
		}
		var digest string
		id := manifest.Descriptor.Identity
		if candidateID != "" && id.ID == candidateID {
			err = lib.Library().Call("directory_digest", map[string]any{"root": with}, &digest)
		} else {
			err = lib.Library().Call("store_digest", map[string]any{"root": m.Store.Root(), "id": id.ID}, &digest)
		}
		if err != nil {
			return report, err
		}
		var state string
		if err := lib.Library().Call("trust_state", map[string]any{"path": filepath.Join(m.Store.Root(), sharedTrustFile), "scope": scope, "id": id.ID, "digest": digest}, &state); err != nil {
			return report, err
		}
		if state != "trusted" {
			report.Unapproved = append(report.Unapproved, id)
		}
	}
	return report, nil
}

// checkRequirements never approves a dependency. Existing stores without any
// requirements need no planner, preserving their current management workflow.
func (m Manager) checkRequirements() error {
	installed, err := m.Store.List()
	if err != nil {
		return err
	}
	for _, p := range installed {
		if hasRequirements(p.Manifest) {
			report, err := m.resolvePlan("", "")
			if err != nil {
				return err
			}
			if report.Status != "ok" {
				return fmt.Errorf("unresolved package dependencies (%s); run ext package plan", report.Status)
			}
			return nil
		}
	}
	return nil
}

// CheckInvocation verifies resolution and transitive host approval before a process
// starts, so a revoked provider cannot be used via an already approved consumer.
func (m Manager) CheckInvocation(id string) error {
	report, err := m.resolvePlan("", "")
	if err != nil {
		return err
	}
	if report.Status != "ok" {
		return fmt.Errorf("unresolved package dependencies (%s); run ext package plan", report.Status)
	}
	needed := map[string]bool{id: true}
	for changed := true; changed; {
		changed = false
		for _, binding := range report.Plan.Bindings {
			if needed[binding.Consumer.ID] && !needed[binding.Provider.ID] {
				needed[binding.Provider.ID] = true
				changed = true
			}
		}
	}
	for _, unapproved := range report.Unapproved {
		if needed[unapproved.ID] {
			return fmt.Errorf("required package %s is not approved; review it before ext package trust", unapproved.ID)
		}
	}
	return nil
}

// Adapter dependency metadata requires planning even when generic requires is empty.
func hasRequirements(m packagekit.Manifest) bool {
	if len(m.Requires) > 0 {
		return true
	}
	return adapter.HasDependencyDeclarations(m)
}
