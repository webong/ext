package adapter

import (
	"encoding/json"
	"errors"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

func dependencyPackage(id, revision string, metadata string, ops ...string) packagekit.Manifest {
	c := plugin.Contract{ContractRef: plugin.ContractRef{Name: plugin.AdapterContractName, Version: APIVersion}}
	for _, op := range ops {
		c.Operations = append(c.Operations, plugin.Operation{Name: op})
	}
	return packagekit.Manifest{Descriptor: plugin.Descriptor{Identity: plugin.Identity{ID: id, Revision: revision}, Contracts: []plugin.Contract{c}}, Metadata: map[string]json.RawMessage{MetadataKey: json.RawMessage(metadata)}}
}
func TestProjectDependencies(t *testing.T) {
	empty, err := ProjectDependencies([]packagekit.Manifest{}, "linux")
	if err != nil || empty == nil {
		t.Fatalf("empty graph must encode as an array: %v", err)
	}
	consumer := dependencyPackage("consumer", "c1", `{"dependencies_linux":["storage:provider@2.0"]}`, "run")
	provider := dependencyPackage("provider", "p1", `{"share_spaces":["storage"]}`, "share")
	graph := []packagekit.Manifest{consumer, provider}
	projected, err := ProjectDependencies(graph, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if len(graph[0].Requires) != 0 {
		t.Fatal("mutated stored manifest")
	}
	edge := projected[0].Requires[0]
	if edge.Provider != provider.Descriptor.Identity.ID || edge.Identity != nil || edge.Operation != "share" {
		t.Fatalf("wrong edge: %+v", edge)
	}
	again, err := ProjectDependencies(projected, "linux")
	if err != nil || len(again[0].Requires) != 1 {
		t.Fatalf("duplicate projection: %v %+v", err, again)
	}
	for _, conditional := range []packagekit.Dependency{
		{Provider: edge.Provider, Contract: edge.Contract, Operation: edge.Operation, OS: "darwin"},
		{Provider: edge.Provider, Contract: edge.Contract, Operation: edge.Operation, Arch: "otherarch"},
	} {
		narrow := append([]packagekit.Manifest(nil), graph...)
		narrow[0].Requires = []packagekit.Dependency{conditional}
		got, err := ProjectDependencies(narrow, "linux")
		if err != nil || len(got[0].Requires) != 2 || got[0].Requires[1].OS != "" || got[0].Requires[1].Arch != "" {
			t.Fatalf("conditional declaration weakened adapter obligation: %v %+v", err, got)
		}
	}
	graph[1].Descriptor.Identity.Revision = "p2"
	upgraded, err := ProjectDependencies(graph, "linux")
	if err != nil || upgraded[0].Requires[0].Provider != provider.Descriptor.Identity.ID || upgraded[0].Requires[0].Identity != nil {
		t.Fatalf("upgrade pinned old revision: %v", err)
	}
	other, err := ProjectDependencies(graph, "darwin")
	if err != nil || len(other[0].Requires) != 0 {
		t.Fatalf("foreign platform edge: %v", err)
	}
	if _, err := ProjectDependencies(graph[:1], "linux"); !errors.Is(err, plugin.ErrNotFound) {
		t.Fatalf("missing provider: %v", err)
	}
	graph[1].Metadata[MetadataKey] = json.RawMessage(`{"share_spaces":["other"]}`)
	if _, err := ProjectDependencies(graph, "linux"); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatalf("share space weakened: %v", err)
	}
	graph[1] = provider
	graph[1].Descriptor.Contracts = []plugin.Contract{{ContractRef: plugin.ContractRef{Name: plugin.AdapterContractName, Version: APIVersion}, Operations: []plugin.Operation{{Name: "run"}}}}
	if _, err := ProjectDependencies(graph, "linux"); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatalf("missing share operation: %v", err)
	}
	if !HasDependencyDeclarations(consumer) || HasDependencyDeclarations(provider) {
		t.Fatal("wrong preflight detection")
	}
}

func TestDeclaredShareDependencies(t *testing.T) {
	consumer := dependencyPackage("consumer", "c1", `{"share_dependencies":["storage:provider","storage:alternate"]}`, "run")
	consumer.Requires = []packagekit.Dependency{
		{Provider: "provider", Contract: plugin.ContractRef{Name: plugin.AdapterContractName, Version: APIVersion}, Operation: "share", OS: "linux", Arch: "arm64"},
		{Provider: "alternate", Contract: plugin.ContractRef{Name: plugin.AdapterContractName, Version: APIVersion}, Operation: "share", OS: "darwin"},
	}
	for _, test := range []struct{ os, arch, provider string }{{"linux", "arm64", "provider"}, {"linux", "amd64", ""}, {"darwin", "amd64", "alternate"}, {"windows", "arm64", ""}} {
		values := map[string]string{}
		if err := metadataValues(consumer.Metadata[MetadataKey], values); err != nil {
			t.Fatal(err)
		}
		got, err := declaredDependencies(consumer, values, test.os, test.arch)
		if err != nil || got["storage"].Adapter != test.provider {
			t.Fatalf("%s/%s: %v %v", test.os, test.arch, got, err)
		}
	}
	for _, test := range []struct {
		name   string
		modify func(*packagekit.Manifest, map[string]string)
	}{
		{"orphan", func(m *packagekit.Manifest, v map[string]string) { m.Requires = nil }},
		{"mixed", func(m *packagekit.Manifest, v map[string]string) { v["dependencies_linux"] = "storage:provider@2.0" }},
		{"bad-binding", func(m *packagekit.Manifest, v map[string]string) { v["share_dependencies"] = "storage:provider@2.0" }},
		{"duplicate", func(m *packagekit.Manifest, v map[string]string) {
			v["share_dependencies"] = "storage:provider,storage:provider"
		}},
		{"unsupported-inactive", func(m *packagekit.Manifest, v map[string]string) { m.Requires[1].Contract.Version = "3.0" }},
		{"overlap-inactive", func(m *packagekit.Manifest, v map[string]string) { m.Requires[1].OS = "linux" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := consumer
			m.Requires = append([]packagekit.Dependency(nil), consumer.Requires...)
			v := map[string]string{}
			if err := metadataValues(m.Metadata[MetadataKey], v); err != nil {
				t.Fatal(err)
			}
			test.modify(&m, v)
			if _, err := declaredDependencies(m, v, "windows", "amd64"); err == nil {
				t.Fatal("invalid mapping accepted on inactive platform")
			}
		})
	}
}
