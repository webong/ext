package graph

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/webong/ext/res/graph/store"
	"github.com/webong/ext/res/graph/store/storetest"
	proc "github.com/webong/ext/res/system/service/process"
	"github.com/webong/ext/res/system/service/program"
)

// The recorder fixtures (testdata/recorder/v1) hold the projection of
// observations onto the ext.system vocabulary as data, so lib/ledger can be held
// to what this Go recorder does before it is removed. -update writes what Go
// records into each step's expect.
var updateRecorderFixtures = flag.Bool("update", false, "rewrite the expectations of testdata/recorder/v1/cases.json")

const recorderFixtures = "testdata/recorder/v1"

type fixtureEnvironment struct {
	Now             time.Time         `json:"now"`
	Hostname        string            `json:"hostname"`
	OS              string            `json:"os"`
	Architecture    string            `json:"architecture"`
	ParentPID       int               `json:"parent_pid"`
	WorkingDir      string            `json:"working_directory"`
	Variables       map[string]string `json:"variables"`
	Paths           []string          `json:"paths"`
	HostEnvironment string            `json:"host_environment"`
}

func (f fixtureEnvironment) environment() *environment {
	paths := map[string]bool{}
	for _, p := range f.Paths {
		paths[filepath.Clean(p)] = true
	}
	return &environment{
		now:             func() time.Time { return f.Now },
		hostname:        func() string { return f.Hostname },
		goos:            f.OS,
		goarch:          f.Architecture,
		parentPID:       func() int { return f.ParentPID },
		workingDir:      func() string { return f.WorkingDir },
		getenv:          func(key string) string { return f.Variables[key] },
		exists:          func(path string) bool { return paths[filepath.Clean(path)] },
		hostEnvironment: func() string { return f.HostEnvironment },
	}
}

// The wire forms of the inventory observations, which have no JSON names in Go.
type fixtureAdapter struct {
	Name             string                `json:"name"`
	Runtime          string                `json:"runtime"`
	Selector         string                `json:"selector"`
	DiscoveryStatus  string                `json:"discovery_status"`
	Surfaces         []string              `json:"surfaces"`
	Capabilities     []string              `json:"capabilities"`
	Supports         []string              `json:"supports"`
	Engine           []string              `json:"engine"`
	Role             []string              `json:"role"`
	BrowserShare     []string              `json:"browser_share"`
	Trusted          bool                  `json:"trusted"`
	ListSupported    bool                  `json:"list_supported"`
	ObserveSupported bool                  `json:"observe_supported"`
	Listed           bool                  `json:"listed"`
	Contexts         []ContextObservation  `json:"contexts"`
	Programs         []program.Candidate   `json:"programs"`
	Resources        []fixtureResource     `json:"resources"`
	Relations        []RelationObservation `json:"-"`
	RelationList     []struct {
		From string `json:"from"`
		To   string `json:"to"`
		Kind string `json:"kind"`
	} `json:"relations"`
}

type fixtureResource struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Context    string         `json:"context"`
	Attributes map[string]any `json:"attributes"`
}

type fixtureAlias struct {
	Space     string            `json:"space"`
	Name      string            `json:"name"`
	Adapter   string            `json:"adapter"`
	Selection string            `json:"selection"`
	Metadata  map[string]string `json:"metadata"`
}

type fixtureInstaller struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

func (a fixtureAdapter) observation() AdapterObservation {
	o := AdapterObservation{Name: a.Name, Runtime: a.Runtime, Selector: a.Selector, DiscoveryStatus: a.DiscoveryStatus,
		Surfaces: a.Surfaces, Capabilities: a.Capabilities, Supports: a.Supports, Engine: a.Engine, Role: a.Role,
		BrowserShare: a.BrowserShare, Trusted: a.Trusted, ListSupported: a.ListSupported, ObserveSupported: a.ObserveSupported,
		Listed: a.Listed, Contexts: a.Contexts, Programs: a.Programs}
	for _, r := range a.Resources {
		o.Resources = append(o.Resources, ResourceObservation{ID: r.ID, Kind: r.Kind, Context: r.Context, Attributes: r.Attributes})
	}
	for _, r := range a.RelationList {
		o.Relations = append(o.Relations, RelationObservation{From: r.From, To: r.To, Kind: r.Kind})
	}
	return o
}

type recorderStep struct {
	Op     string          `json:"op"`
	Input  json.RawMessage `json:"input,omitempty"`
	Error  string          `json:"error,omitempty"`
	Expect json.RawMessage `json:"expect,omitempty"`
}

type recorderCase struct {
	Name        string             `json:"name"`
	About       string             `json:"about,omitempty"`
	Environment fixtureEnvironment `json:"environment"`
	Steps       []recorderStep     `json:"steps"`
}

type recorderFile struct {
	Version int            `json:"version"`
	About   string         `json:"about"`
	Cases   []recorderCase `json:"cases"`
}

func runRecorderStep(ctx context.Context, g *Graph, st recorderStep) (any, error) {
	decode := func(v any) error {
		if len(st.Input) == 0 {
			return nil
		}
		return json.Unmarshal(st.Input, v)
	}
	switch st.Op {
	case "observe_shell":
		var in struct {
			Cwd        string            `json:"cwd"`
			Profile    string            `json:"profile"`
			Selections map[string]string `json:"selections"`
		}
		if err := decode(&in); err != nil {
			return nil, err
		}
		return nil, g.ObserveShell(ctx, in.Cwd, in.Profile, in.Selections)
	case "observe_web":
		var in struct {
			Provider string `json:"provider"`
			Profile  string `json:"profile"`
			Scheme   string `json:"scheme"`
			Host     string `json:"host"`
		}
		if err := decode(&in); err != nil {
			return nil, err
		}
		return nil, g.ObserveWeb(ctx, in.Provider, in.Profile, in.Scheme, in.Host)
	case "observe_host":
		var in HostInventory
		if err := decode(&in); err != nil {
			return nil, err
		}
		return nil, g.ObserveHost(ctx, in)
	case "observe_processes":
		var in proc.ProcessSnapshot
		if err := decode(&in); err != nil {
			return nil, err
		}
		return nil, g.ObserveProcesses(ctx, in)
	case "observe_inventory":
		var in struct {
			Adapters   []fixtureAdapter   `json:"adapters"`
			Aliases    []fixtureAlias     `json:"aliases"`
			Installers []fixtureInstaller `json:"installers"`
		}
		if err := decode(&in); err != nil {
			return nil, err
		}
		adapters := make([]AdapterObservation, 0, len(in.Adapters))
		for _, a := range in.Adapters {
			adapters = append(adapters, a.observation())
		}
		var aliases []AliasObservation
		for _, a := range in.Aliases {
			aliases = append(aliases, AliasObservation{Space: a.Space, Name: a.Name, Adapter: a.Adapter, Selection: a.Selection, Metadata: a.Metadata})
		}
		var installers []InstallerObservation
		for _, i := range in.Installers {
			installers = append(installers, InstallerObservation{Name: i.Name, Source: i.Source})
		}
		return nil, g.ObserveInventory(ctx, adapters, aliases, installers...)
	case "snapshot":
		return g.Store.Snapshot(ctx, Namespace)
	case "resolve_inventory":
		var in struct {
			Runtime string `json:"runtime"`
		}
		if err := decode(&in); err != nil {
			return nil, err
		}
		return g.ResolveInventory(ctx, in.Runtime)
	case "read_host":
		return g.ReadHost(ctx)
	}
	return nil, &unknownStep{st.Op}
}

type unknownStep struct{ op string }

func (u *unknownStep) Error() string { return "unknown recorder step " + u.op }

// The fields an expectation leaves out because the store, not the recorder, sets them.
var storeFields = map[string]bool{"revision": true, "created_at": true, "updated_at": true, "namespace": true}

func pruneStoreFields(v any, depth int) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			if depth > 0 && storeFields[k] {
				continue
			}
			out[k] = pruneStoreFields(val, depth+1)
		}
		return out
	case []any:
		for i := range x {
			x[i] = pruneStoreFields(x[i], depth+1)
		}
		return x
	}
	return v
}

func asFixtureJSON(v any) (any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	return out, json.Unmarshal(data, &out)
}

func TestRecorderFixtures(t *testing.T) {
	path := filepath.Join(recorderFixtures, "cases.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file recorderFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if file.Version != 1 || len(file.Cases) == 0 {
		t.Fatalf("%s: version %d with %d cases", path, file.Version, len(file.Cases))
	}
	if runtime.GOOS == "windows" {
		t.Skip("the v1 recorder cases use POSIX paths")
	}
	for ci := range file.Cases {
		c := &file.Cases[ci]
		t.Run(c.Name, func(t *testing.T) {
			ctx := context.Background()
			g, err := New(store.NewMemory())
			if err != nil {
				t.Fatal(err)
			}
			g.env = c.Environment.environment()
			for i := range c.Steps {
				st := &c.Steps[i]
				got, err := runRecorderStep(ctx, g, *st)
				if _, unknown := err.(*unknownStep); unknown {
					t.Fatal(err)
				}
				code := ""
				if err != nil {
					code = store.Code(err)
					if code == "io" { // the recorder's own refusals are plain errors
						code = "invalid"
					}
				}
				if *updateRecorderFixtures {
					st.Error = code
					if err == nil && got != nil {
						actual, err := asFixtureJSON(got)
						if err != nil {
							t.Fatal(err)
						}
						encoded, _ := json.Marshal(pruneStoreFields(actual, 0))
						st.Expect = encoded
					}
					continue
				}
				if code != st.Error {
					t.Fatalf("step %d (%s): code %q, want %q (%v)", i, st.Op, code, st.Error, err)
				}
				if err != nil || st.Expect == nil {
					continue
				}
				var want any
				if err := json.Unmarshal(st.Expect, &want); err != nil {
					t.Fatalf("step %d: expect: %v", i, err)
				}
				actual, err := asFixtureJSON(got)
				if err != nil {
					t.Fatal(err)
				}
				if !storetest.Match(want, actual) {
					encoded, _ := json.Marshal(actual)
					t.Fatalf("step %d (%s):\n got %s\nwant %s", i, st.Op, encoded, st.Expect)
				}
			}
		})
	}
	if *updateRecorderFixtures {
		writeRecorderFixtures(t, path, file)
	}
}

// writeRecorderFixtures writes the cases one step per line, so a diff shows which step changed.
func writeRecorderFixtures(t *testing.T, path string, file recorderFile) {
	var b strings.Builder
	head, _ := json.Marshal(file.About)
	b.WriteString("{\n  \"version\": 1,\n  \"about\": " + string(head) + ",\n  \"cases\": [\n")
	for ci, c := range file.Cases {
		name, _ := json.Marshal(c.Name)
		env, _ := json.Marshal(c.Environment)
		b.WriteString("    {\n      \"name\": " + string(name) + ",\n")
		if c.About != "" {
			about, _ := json.Marshal(c.About)
			b.WriteString("      \"about\": " + string(about) + ",\n")
		}
		b.WriteString("      \"environment\": " + string(env) + ",\n      \"steps\": [\n")
		for i, st := range c.Steps {
			line, _ := json.Marshal(st)
			b.WriteString("        " + string(line))
			if i < len(c.Steps)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString("      ]\n    }")
		if ci < len(file.Cases)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  ]\n}\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The vocabulary fixture is Schema(), normalised.
func TestRecorderVocabularyFixture(t *testing.T) {
	path := filepath.Join(recorderFixtures, "vocabulary.json")
	if *updateRecorderFixtures {
		schema := store.NormalizeSchema(Schema())
		encoded, _ := json.MarshalIndent(map[string]any{
			"version": 1,
			"about":   "The ext.system vocabulary the recorder registers: every vertex kind, and every relationship with the kinds of its endpoints. A store checks the recorder's transactions against it.",
			"schema":  schema,
		}, "", "  ")
		if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Schema store.Schema `json:"schema"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	want, _ := store.Canonical(store.NormalizeSchema(Schema()))
	got, _ := store.Canonical(store.NormalizeSchema(file.Schema))
	if string(got) != string(want) {
		t.Fatalf("vocabulary.json is not Schema():\n got %s\nwant %s", got, want)
	}
	kinds := append([]string(nil), file.Schema.Kinds...)
	if !sort.StringsAreSorted(kinds) {
		t.Fatal("vocabulary.json lists its kinds out of order")
	}
}
