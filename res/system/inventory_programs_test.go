package system

import (
	"context"
	"reflect"
	"testing"

	"github.com/webong/ext/res/system/graph"
	"github.com/webong/ext/res/system/service/program"
)

func programAdapters() []AdapterObservation {
	return []AdapterObservation{
		{Name: "homebrew", Runtime: "computer", Trusted: true, Listed: true, Role: []string{"installer"}},
		{
			Name: "python", Runtime: "computer", Trusted: true, Listed: true, Supports: []string{"python"},
			Contexts: []ContextObservation{
				{Selection: "3.14", Attributes: map[string]any{"executable": "/opt/homebrew/opt/python@3.14/bin/python3.14", "suggested_installer": "homebrew"}},
				{Selection: "3.9", Attributes: map[string]any{"executable": "/usr/bin/python3", "suggested_installer": "xcode"}},
				{Selection: "2.7", Attributes: map[string]any{"executable": "/gone/python"}},
			},
			Programs: []program.Candidate{
				{Name: "python3", Path: "/opt/homebrew/bin/python3", RealPath: "/opt/homebrew/opt/python@3.14/bin/python3.14", Version: "3.14.5", OnPath: true, Installers: []string{"homebrew", "asdf"}},
				{Name: "python3.14", Path: "/opt/homebrew/opt/python@3.14/bin/python3.14", RealPath: "/opt/homebrew/opt/python@3.14/bin/python3.14", Installers: []string{"homebrew"}},
				{Name: "python3", Path: "/usr/bin/python3", RealPath: "/usr/bin/python3", Version: "3.9.6", OnPath: true},
			},
		},
	}
}

func TestInventoryProjectsPrograms(t *testing.T) {
	g, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := g.ObserveInventory(ctx, programAdapters(), nil, InstallerObservation{Name: "mytools", Source: "config"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := g.Store.Snapshot(ctx, Namespace)
	if err != nil {
		t.Fatal(err)
	}
	vertices := map[string]graph.Vertex{}
	programs := 0
	for _, vertex := range snapshot.Vertices {
		vertices[vertex.ID] = vertex
		if vertex.Kind == Namespace+"/program" {
			programs++
		}
	}
	if programs != 2 {
		t.Fatalf("program vertices = %d, want one per real file (2)", programs)
	}
	brew := vertices["program/"+digest("/opt/homebrew/opt/python@3.14/bin/python3.14")]
	if !reflect.DeepEqual(stringList(brew.Attributes["names"]), []string{"python3", "python3.14"}) || !reflect.DeepEqual(stringList(brew.Attributes["installers"]), []string{"asdf", "homebrew"}) {
		t.Fatalf("merged program = %#v", brew.Attributes)
	}
	var installedBy, hasProgram, uses []graph.Edge
	for _, edge := range snapshot.Edges {
		switch edge.Type {
		case Namespace + "/installed-by":
			installedBy = append(installedBy, edge)
		case Namespace + "/has-program":
			hasProgram = append(hasProgram, edge)
		case Namespace + "/uses-program":
			uses = append(uses, edge)
		}
	}
	// Every installer a program points at is a vertex, so no edge dangles: homebrew is
	// an adapter, asdf is a name nobody declared (the user recognised it), and the
	// system Python has none because only PATH reported it.
	if len(installedBy) != 2 {
		t.Fatalf("installed-by = %#v", installedBy)
	}
	for _, edge := range installedBy {
		if edge.From != brew.ID {
			t.Fatalf("installed-by from %s, want only the Homebrew-reported file", edge.From)
		}
	}
	wantSources := map[string][]string{"homebrew": {"adapter"}, "asdf": {"recognised"}, "mytools": {"config"}}
	installers := map[string]graph.Vertex{}
	for _, vertex := range snapshot.Vertices {
		if vertex.Kind == Namespace+"/installer" {
			installers[vertex.Attributes["name"].(string)] = vertex
		}
	}
	if len(installers) != len(wantSources) {
		t.Fatalf("installer vertices = %d, want %d", len(installers), len(wantSources))
	}
	for name, want := range wantSources {
		if got := stringList(installers[name].Attributes["sources"]); !reflect.DeepEqual(got, want) {
			t.Fatalf("installer %s sources = %v, want %v", name, got, want)
		}
	}
	var implementedBy int
	for _, edge := range snapshot.Edges {
		if edge.Type == Namespace+"/implemented-by" {
			implementedBy++
			if edge.From != installerID("homebrew") || edge.To != "adapter/homebrew" {
				t.Fatalf("implemented-by = %#v", edge)
			}
		}
	}
	if implementedBy != 1 {
		t.Fatalf("implemented-by edges = %d, want one (only an adapter installer has an adapter)", implementedBy)
	}
	if !reflect.DeepEqual(stringList(installers["homebrew"].Attributes["sources"]), []string{"adapter"}) || installers["homebrew"].Attributes["adapter"] != "homebrew" {
		t.Fatalf("homebrew = %#v", installers["homebrew"].Attributes)
	}
	if len(hasProgram) != 2 || len(uses) != 2 {
		t.Fatalf("has-program = %d, uses-program = %d, want 2 and 2 (the context for /gone/python has no program)", len(hasProgram), len(uses))
	}

	inventory, err := g.ResolveInventory(ctx, "computer")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]*ContextProgram{}
	for _, candidate := range inventory.Contexts {
		found[candidate.Selection] = candidate.Program
	}
	if p := found["3.14"]; p == nil || p.Version != "3.14.5" || !reflect.DeepEqual(p.Installers, []string{"asdf", "homebrew"}) || p.Path != "/opt/homebrew/bin/python3" {
		t.Fatalf("3.14 program = %#v", p)
	}
	if p := found["3.9"]; p == nil || len(p.Installers) != 0 || !p.OnPath {
		t.Fatalf("3.9 program = %#v, want a PATH-only program with no installer", p)
	}
	// A suggestion is kept apart from the facts, and one already confirmed by an
	// installer is not repeated. It never becomes an edge.
	if p := found["3.9"]; !reflect.DeepEqual(p.Suggested, []string{"xcode"}) {
		t.Fatalf("3.9 suggested = %v, want [xcode]", p.Suggested)
	}
	if p := found["3.14"]; len(p.Suggested) != 0 {
		t.Fatalf("3.14 suggested = %v, want none: homebrew already reported it", p.Suggested)
	}
	if found["2.7"] != nil {
		t.Fatalf("2.7 program = %#v, want none", found["2.7"])
	}

	// A program that is no longer reported leaves the graph with its edges.
	adapters := programAdapters()
	adapters[1].Programs = adapters[1].Programs[2:]
	if err := g.ObserveInventory(ctx, adapters, nil); err != nil {
		t.Fatal(err)
	}
	snapshot, err = g.Store.Snapshot(ctx, Namespace)
	if err != nil {
		t.Fatal(err)
	}
	for _, vertex := range snapshot.Vertices {
		if vertex.ID == brew.ID {
			t.Fatalf("program %s remained after it stopped being reported", vertex.ID)
		}
	}
	for _, edge := range snapshot.Edges {
		if edge.Type == Namespace+"/installed-by" || edge.To == brew.ID {
			t.Fatalf("edge %s remained for a removed program", edge.ID)
		}
	}
	// A name only a program pointed at goes with it; the declared and adapter ones stay.
	for _, vertex := range snapshot.Vertices {
		if vertex.Kind == Namespace+"/installer" && vertex.Attributes["name"] == "asdf" {
			t.Fatal("the recognised-only installer remained after its program went")
		}
	}
}
