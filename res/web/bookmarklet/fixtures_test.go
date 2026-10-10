package bookmarklet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Language-neutral vectors shared with other implementations; see
// ../testdata/bookmarklet/README.md.
const fixtureDir = "../testdata/bookmarklet/v1alpha1"

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

func TestFixtureEncode(t *testing.T) {
	var f struct {
		Cases   []struct{ Name, Source, URL string } `json:"cases"`
		Rejects []struct{ Name, Source string }      `json:"rejects"`
	}
	load(t, "encode.json", &f)
	if len(f.Cases) == 0 || len(f.Rejects) == 0 {
		t.Fatal("missing cases")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := Encode(c.Source)
			if err != nil || got != c.URL {
				t.Fatalf("got %q, %v\nwant %q", got, err, c.URL)
			}
		})
	}
	for _, c := range f.Rejects {
		t.Run("rejects "+c.Name, func(t *testing.T) {
			if got, err := Encode(c.Source); err == nil {
				t.Fatalf("accepted: %q", got)
			}
		})
	}
}

func TestFixtureDecode(t *testing.T) {
	var f struct {
		Cases   []struct{ Name, URL, Source string } `json:"cases"`
		Rejects []struct{ Name, URL string }         `json:"rejects"`
	}
	load(t, "decode.json", &f)
	if len(f.Cases) == 0 || len(f.Rejects) == 0 {
		t.Fatal("missing cases")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := Decode(c.URL)
			if err != nil || got != c.Source {
				t.Fatalf("got %q, %v\nwant %q", got, err, c.Source)
			}
		})
	}
	for _, c := range f.Rejects {
		t.Run("rejects "+c.Name, func(t *testing.T) {
			if got, err := Decode(c.URL); err == nil {
				t.Fatalf("accepted: %q", got)
			}
		})
	}
}

func TestFixtureInstallPage(t *testing.T) {
	var f struct {
		Cases []struct {
			Name     string   `json:"name"`
			PageName string   `json:"pageName"`
			Source   string   `json:"source"`
			Contains []string `json:"contains"`
			Absent   []string `json:"absent"`
		} `json:"cases"`
		Rejects []struct {
			Name     string `json:"name"`
			PageName string `json:"pageName"`
			Source   string `json:"source"`
		} `json:"rejects"`
	}
	load(t, "install-page.json", &f)
	if len(f.Cases) == 0 || len(f.Rejects) == 0 {
		t.Fatal("missing cases")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			page, err := InstallPage(c.PageName, c.Source)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range c.Contains {
				if !strings.Contains(page, want) {
					t.Errorf("page lacks %q", want)
				}
			}
			for _, bad := range c.Absent {
				if strings.Contains(page, bad) {
					t.Errorf("page contains %q", bad)
				}
			}
		})
	}
	for _, c := range f.Rejects {
		t.Run("rejects "+c.Name, func(t *testing.T) {
			if _, err := InstallPage(c.PageName, c.Source); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
