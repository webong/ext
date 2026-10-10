package userscript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The fixtures under res/web/testdata/userscript/v1alpha1 are language-neutral:
// other implementations run the same files. See that directory's README.md.
const fixtureDir = "../testdata/userscript/v1alpha1"

func load(t *testing.T, name string, into any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func signed(r Record) Record {
	r.Revision = Revision(r)
	return r
}

func baseRecord() Record {
	return Record{Target: "session", ID: "demo", Name: "demo", Matches: []string{"https://example.com/*"}, ExcludeMatches: []string{}, Source: "console.log(1);\n"}
}

func expectOutcome(t *testing.T, err error, wantCode string) {
	t.Helper()
	if wantCode == "" {
		if err != nil {
			t.Fatalf("want valid, got %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("want error %q, got none", wantCode)
	}
	if got := ErrorCode(err); got != wantCode {
		t.Fatalf("want error code %q, got %q (%v)", wantCode, got, err)
	}
}

func TestFixtureMatchPatterns(t *testing.T) {
	var f struct {
		Cases []struct {
			Name    string `json:"name"`
			Pattern string `json:"pattern"`
			Valid   bool   `json:"valid"`
			Error   string `json:"error"`
		} `json:"cases"`
	}
	load(t, "match-patterns.json", &f)
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			r := baseRecord()
			r.Matches = []string{c.Pattern}
			expectOutcome(t, Validate(signed(r)), c.Error)
			if c.Valid != (c.Error == "") {
				t.Fatal("fixture inconsistent: valid must match the absence of error")
			}
		})
	}
}

func TestFixtureDirectives(t *testing.T) {
	var f struct {
		Cases []struct {
			Name           string   `json:"name"`
			Source         string   `json:"source"`
			Matches        []string `json:"matches"`
			ExcludeMatches []string `json:"excludeMatches"`
			Error          string   `json:"error"`
		} `json:"cases"`
	}
	load(t, "directives.json", &f)
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			r := baseRecord()
			r.Source, r.Matches, r.ExcludeMatches = c.Source, c.Matches, c.ExcludeMatches
			expectOutcome(t, Validate(signed(r)), c.Error)
		})
	}
}

func TestFixtureMetadata(t *testing.T) {
	var f struct {
		Cases []struct {
			Name       string `json:"name"`
			SourceFile string `json:"sourceFile"`
			Source     string `json:"source"`
			Expect     struct {
				Name           string   `json:"name"`
				ID             string   `json:"id"`
				Matches        []string `json:"matches"`
				ExcludeMatches []string `json:"excludeMatches"`
			} `json:"expect"`
		} `json:"cases"`
	}
	load(t, "metadata.json", &f)
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got := InferMetadata(Record{Source: c.Source}, c.SourceFile)
			if got.Name != c.Expect.Name || got.ID != c.Expect.ID ||
				!reflect.DeepEqual(got.Matches, c.Expect.Matches) || !reflect.DeepEqual(got.ExcludeMatches, c.Expect.ExcludeMatches) {
				t.Fatalf("got %+v\nwant %+v", got, c.Expect)
			}
		})
	}
}

type fixtureRecord struct {
	Target         string   `json:"target"`
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Revision       string   `json:"revision"`
	Matches        []string `json:"matches"`
	ExcludeMatches []string `json:"excludeMatches"`
	Source         string   `json:"source"`
}

func (f fixtureRecord) record() Record {
	return Record{Target: f.Target, ID: f.ID, Name: f.Name, Revision: f.Revision, Matches: f.Matches, ExcludeMatches: f.ExcludeMatches, Source: f.Source}
}

func TestFixtureRevision(t *testing.T) {
	var f struct {
		Cases []struct {
			Name   string        `json:"name"`
			Record fixtureRecord `json:"record"`
			Digest string        `json:"revision"`
		} `json:"cases"`
	}
	load(t, "revision.json", &f)
	if len(f.Cases) < 4 {
		t.Fatal("too few cases")
	}
	seen := map[string]string{}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if got := Revision(c.Record.record()); got != c.Digest {
				t.Fatalf("got %s want %s", got, c.Digest)
			}
			if other, dup := seen[c.Digest]; dup {
				t.Fatalf("vectors %q and %q share a digest", other, c.Name)
			}
			seen[c.Digest] = c.Name
		})
	}
}

func TestFixtureRecords(t *testing.T) {
	var f struct {
		Cases []struct {
			Name   string        `json:"name"`
			Record fixtureRecord `json:"record"`
			Error  string        `json:"error"`
		} `json:"cases"`
	}
	load(t, "record.json", &f)
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			r := c.Record.record()
			if r.Revision == "auto" {
				r.Revision = Revision(r)
			}
			expectOutcome(t, Validate(r), c.Error)
		})
	}
}

func TestErrorCodeIgnoresForeignErrors(t *testing.T) {
	if ErrorCode(nil) != "" || ErrorCode(os.ErrNotExist) != "" {
		t.Fatal("foreign errors have no code")
	}
	if !strings.Contains((&Error{Code: "x", Message: "m"}).Error(), "m") {
		t.Fatal("message lost")
	}
}
