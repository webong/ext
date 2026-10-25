package packagekit_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"github.com/webong/ext/pkg/plugin-go/engine/package"
)

// Validate and Resolve run on manifests a peer controls, so their cost must not
// depend on comparing every artifact, or every manifest, with every other. These
// tests hold them to the old pairwise definitions on random small inputs, and to
// a time bound at the documented limits that the old code missed by minutes.

// overlapsReference is the original tree-overlap rule, applied to every pair.
func overlapsReference(artifacts []packagekit.Artifact) bool {
	for _, tree := range artifacts {
		if tree.Kind != packagekit.ArtifactTree {
			continue
		}
		prefix := strings.ToLower(tree.Path)
		for _, other := range artifacts {
			if other.Name == tree.Name {
				continue
			}
			op := strings.ToLower(other.Path)
			if op == prefix || strings.HasPrefix(op, prefix+"/") || strings.HasPrefix(prefix, op+"/") {
				return true
			}
		}
	}
	return false
}

func TestTreeOverlapMatchesPairwiseRule(t *testing.T) {
	parts := []string{"a", "A", "b", "ä", "Ä", "a-b", "a+b", "a b", "x.y", "ǅ", "ǆ"}
	sum := strings.Repeat("0", 64)
	rng := rand.New(rand.NewSource(11))
	var overlapping, clean int
	for round := 0; round < 20000; round++ {
		m := manifest("example/a")
		m.Entrypoints = nil
		m.Artifacts = nil
		seen := map[string]bool{}
		unique := true
		for i, n := 0, 1+rng.Intn(7); i < n; i++ {
			var path []string
			for d, depth := 0, 1+rng.Intn(3); d < depth; d++ {
				path = append(path, parts[rng.Intn(len(parts))])
			}
			a := packagekit.Artifact{Name: fmt.Sprintf("n%d", i), Path: strings.Join(path, "/"), SHA256: sum}
			if rng.Intn(3) == 0 {
				a.Kind = packagekit.ArtifactTree
			}
			if seen[strings.ToLower(a.Path)] {
				unique = false // a repeated path is rejected before overlap is looked at
			}
			seen[strings.ToLower(a.Path)] = true
			m.Artifacts = append(m.Artifacts, a)
		}
		err := m.Validate()
		switch {
		case !unique:
			if !errors.Is(err, plugin.ErrInvalid) {
				t.Fatalf("round %d: repeated path accepted: %v", round, m.Artifacts)
			}
		case overlapsReference(m.Artifacts):
			overlapping++
			if !errors.Is(err, plugin.ErrInvalid) || !strings.Contains(err.Error(), "overlaps") {
				t.Fatalf("round %d: overlap not found (%v): %v", round, err, m.Artifacts)
			}
		default:
			clean++
			if err != nil {
				t.Fatalf("round %d: %v: %v", round, err, m.Artifacts)
			}
		}
	}
	if overlapping < 1000 || clean < 1000 {
		t.Fatalf("lopsided: %d overlapping, %d clean", overlapping, clean)
	}
}

func TestResolveMatchesSelect(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	outcomes := map[string]int{}
	for round := 0; round < 4000; round++ {
		n := 1 + rng.Intn(6)
		manifests := make([]packagekit.Manifest, n)
		for i := range manifests {
			m := manifest(fmt.Sprintf("example/m%d", i))
			m.Descriptor.Contracts = []plugin.Contract{{
				ContractRef: plugin.ContractRef{Name: fmt.Sprintf("c%d", rng.Intn(3)), Version: "v1"},
				Operations:  []plugin.Operation{{Name: "o0"}, {Name: "o1"}},
			}}
			manifests[i] = m
		}
		for i := range manifests {
			for r := 0; r < 3; r++ {
				if rng.Intn(2) == 0 {
					continue
				}
				d := packagekit.Dependency{
					Contract:  plugin.ContractRef{Name: fmt.Sprintf("c%d", rng.Intn(4)), Version: "v1"},
					Operation: fmt.Sprintf("o%d", rng.Intn(2)),
				}
				if rng.Intn(2) == 0 {
					id := manifests[rng.Intn(n)].Descriptor.Identity
					d.Identity = &id
				}
				duplicate := false
				for _, other := range manifests[i].Requires {
					if other.Contract == d.Contract && other.Operation == d.Operation && (other.Identity == nil) == (d.Identity == nil) && (d.Identity == nil || *other.Identity == *d.Identity) {
						duplicate = true
					}
				}
				if !duplicate {
					manifests[i].Requires = append(manifests[i].Requires, d)
				}
			}
		}
		// the reference: plugin.Select for every requirement, as Resolve used to
		descriptors := make([]plugin.Descriptor, n)
		for i, m := range manifests {
			descriptors[i] = m.Descriptor
		}
		var wantErr error
		for _, m := range manifests {
			for _, r := range m.Requires {
				if _, err := plugin.Select(descriptors, plugin.Requirement{Contract: r.Contract, Operation: r.Operation, Identity: r.Identity}); err != nil && wantErr == nil {
					wantErr = err
				}
			}
		}
		plan, err := packagekit.Resolve(manifests)
		if wantErr != nil {
			outcomes["error"]++
			if !errors.Is(err, wantErr) {
				t.Fatalf("round %d: Resolve %v, Select %v", round, err, wantErr)
			}
			continue
		}
		if err != nil {
			if !errors.Is(err, plugin.ErrInvalid) { // a dependency cycle
				t.Fatalf("round %d: %v", round, err)
			}
			outcomes["cycle"]++
			continue
		}
		outcomes["plan"]++
		total := 0
		for _, m := range manifests {
			total += len(m.Requires)
		}
		if len(plan.Bindings) != total || len(plan.Order) != n {
			t.Fatalf("round %d: %d bindings for %d requirements, %d in order", round, len(plan.Bindings), total, len(plan.Order))
		}
		for _, b := range plan.Bindings {
			want, err := plugin.Select(descriptors, plugin.Requirement{Contract: b.Requirement.Contract, Operation: b.Requirement.Operation, Identity: b.Requirement.Identity})
			if err != nil || want.Identity != b.Provider {
				t.Fatalf("round %d: provider %v, Select %v (%v)", round, b.Provider, want.Identity, err)
			}
		}
	}
	for _, kind := range []string{"error", "cycle", "plan"} {
		if outcomes[kind] < 100 {
			t.Fatalf("lopsided outcomes: %v", outcomes)
		}
	}
}

func TestValidateAndResolveStayFastAtTheLimits(t *testing.T) {
	sum := strings.Repeat("0", 64)
	prefix := strings.Repeat("a", 900)
	for _, tree := range []bool{false, true} {
		m := manifest("example/a")
		m.Entrypoints = nil
		m.Artifacts = nil
		for i := 0; i < 4096; i++ {
			a := packagekit.Artifact{Name: fmt.Sprintf("art%d", i), Path: fmt.Sprintf("%s/f%d", prefix, i), SHA256: sum}
			if tree {
				a.Kind, a.Path = packagekit.ArtifactTree, fmt.Sprintf("%s%d", prefix, i)
			}
			m.Artifacts = append(m.Artifacts, a)
		}
		start := time.Now()
		err := m.Validate()
		if err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("tree=%v: validating 4096 artifacts took %v", tree, elapsed)
		}
	}
	manifests := make([]packagekit.Manifest, 512)
	for i := range manifests {
		manifests[i] = manifest(fmt.Sprintf("example/m%d", i))
		for k := 0; k < i && k < 128; k++ {
			manifests[i].Requires = append(manifests[i].Requires, packagekit.Dependency{
				Contract:  plugin.ContractRef{Name: fmt.Sprintf("example/m%d", k), Version: "v1"},
				Operation: "read",
			})
		}
	}
	start := time.Now()
	if _, err := packagekit.Resolve(manifests); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("resolving 512 manifests took %v", elapsed)
	}
}
