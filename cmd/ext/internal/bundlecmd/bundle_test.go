package bundlecmd

import (
	"context"
	"encoding/json"
	"errors"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInterruptedPublicationReconcilesEveryPackage(t *testing.T) {
	m := Manager{Home: t.TempDir()}
	digest := strings.Repeat("a", 64)
	order := []plugin.Identity{{ID: "provider"}, {ID: "consumer"}}
	published := map[string]bool{}
	err := m.publish(order, digest, func(id string) error {
		if id == "consumer" {
			return context.Canceled
		}
		published[id] = true
		return nil
	}, io.Discard)
	if !errors.Is(err, context.Canceled) || !published["provider"] || published["consumer"] {
		t.Fatalf("interruption: %v %v", err, published)
	}
	readJournal := func() journal {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(m.Home, "bundles", digest+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var j journal
		if err := json.Unmarshal(data, &j); err != nil {
			t.Fatal(err)
		}
		return j
	}
	partial := readJournal()
	if partial.State != "partial" || !reflect.DeepEqual(partial.Completed, []string{"provider"}) {
		t.Fatalf("partial journal: %+v", partial)
	}
	var checked []string
	if err := m.publish(order, digest, func(id string) error { checked = append(checked, id); published[id] = true; return nil }, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(checked, []string{"provider", "consumer"}) {
		t.Fatalf("retry skipped validation: %v", checked)
	}
	complete := readJournal()
	if complete.State != "complete" || len(complete.Completed) != 2 {
		t.Fatalf("completion: %+v", complete)
	}
}

func TestManifestComparisonPreservesNumbers(t *testing.T) {
	if sameJSON(json.RawMessage(`{"n":9007199254740993}`), json.RawMessage(`{"n":9007199254740992}`)) {
		t.Fatal("distinct pinned numbers collapsed")
	}
	if !sameJSON(json.RawMessage(`{"b":2,"a":1}`), json.RawMessage(`{"a":1,"b":2}`)) {
		t.Fatal("object ordering changed pin comparison")
	}
}

func TestArchiveLocations(t *testing.T) {
	for _, source := range []string{"../escape.zip", "https://user:secret@example.org/a.zip", "https://example.org/a.zip?token=x", "http://example.org/a.zip", "%2e%2e/a.zip"} {
		if _, err := archivePath("/tmp/bundle.json", source); err == nil {
			t.Errorf("accepted %s", source)
		}
	}
	if got, err := archivePath(filepath.Join(t.TempDir(), "bundle.json"), "https://example.org/a.zip"); err != nil || got != "https://example.org/a.zip" {
		t.Fatalf("HTTPS archive: %s %v", got, err)
	}
}
