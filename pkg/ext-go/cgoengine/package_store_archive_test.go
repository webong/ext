//go:build ext_cengine && cgo && (darwin || linux || windows)

package cgoengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
)

// The C store's install from an archive is held against the pure-Go store's: every case of the
// shared fixtures (testdata/v1/archive-cases.json, which each store also runs on its own) is played
// through both, and each step must give the same outcome, the same installed manifest bytes and the
// same receipt, and leave the same directory below the root.

type archiveStoreOps interface {
	InstallArchive(archive, sha256, id string, preserveExecutable bool) ([]byte, error)
	Receipt(id string) ([]byte, error)
	Remove(id string) error
	Close()
}

func archiveClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrPackageExists):
		return "exists"
	case errors.Is(err, plugin.ErrNotFound):
		return "not_found"
	case errors.Is(err, plugin.ErrMismatch):
		return "mismatch"
	case errors.Is(err, plugin.ErrUnsupported):
		return "unsupported"
	case errors.Is(err, plugin.ErrInvalid):
		return "invalid"
	case errors.Is(err, os.ErrNotExist):
		return "missing"
	}
	return "failed"
}

type archiveFixture struct {
	Cases []struct {
		Name   string `json:"name"`
		Limits struct {
			MaxArchiveBytes  int64 `json:"maxArchiveBytes"`
			MaxTreeFiles     int   `json:"maxTreeFiles"`
			MaxTreeBytes     int64 `json:"maxTreeBytes"`
			MaxArtifactBytes int64 `json:"maxArtifactBytes"`
		} `json:"limits"`
		Steps []struct {
			Install string  `json:"install"`
			Receipt string  `json:"receipt"`
			Remove  string  `json:"remove"`
			Damage  string  `json:"damage"`
			Path    string  `json:"path"`
			SHA256  *string `json:"sha256"`
			ID      string  `json:"id"`
			Status  string  `json:"status"`
		} `json:"steps"`
	} `json:"cases"`
}

func TestInstallArchiveMatchesGo(t *testing.T) {
	fixtures := filepath.Join("_csrc", "testdata", "v1")
	data, err := os.ReadFile(filepath.Join(fixtures, "archive-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture archiveFixture
	if err := json.Unmarshal(data, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatal(err, len(fixture.Cases))
	}
	stores := []struct {
		name string
		open func(root string, l PackageStoreLimits) (archiveStoreOps, error)
	}{
		{"go", func(root string, l PackageStoreLimits) (archiveStoreOps, error) { return openGoPackageStore(root, l) }},
		{"c", func(root string, l PackageStoreLimits) (archiveStoreOps, error) { return OpenPackageStore(root, l) }},
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			limits := PackageStoreLimits{MaxArchiveBytes: c.Limits.MaxArchiveBytes, MaxTreeFiles: c.Limits.MaxTreeFiles,
				MaxTreeBytes: c.Limits.MaxTreeBytes, MaxArtifactBytes: c.Limits.MaxArtifactBytes}
			var logs, trees [2][]string
			for i, kind := range stores {
				root := filepath.Join(t.TempDir(), "plugins")
				s, err := kind.open(root, limits)
				if err != nil {
					t.Fatal(err)
				}
				for n, step := range c.Steps {
					var line string
					switch {
					case step.Install != "":
						sum := ""
						if step.SHA256 != nil {
							sum = *step.SHA256
						}
						manifest, err := s.InstallArchive(filepath.Join(fixtures, filepath.FromSlash(step.Install)), sum, step.ID, false)
						line = fmt.Sprintf("install: %s %q", archiveClass(err), manifest)
						if want := step.Status; archiveClass(err) != want && !(want == "" && err == nil) {
							t.Errorf("%s store, step %d: %s, the fixture says %q", kind.name, n, archiveClass(err), want)
						}
					case step.Receipt != "":
						receipt, err := s.Receipt(step.Receipt)
						var fields map[string]string
						if err == nil {
							if err := json.Unmarshal(receipt, &fields); err != nil {
								t.Fatalf("%s store: receipt %q: %v", kind.name, receipt, err)
							}
						}
						line = fmt.Sprintf("receipt: %s %v", archiveClass(err), fields)
					case step.Remove != "":
						line = "remove: " + archiveClass(s.Remove(step.Remove))
					case step.Damage != "":
						path := filepath.Join(root, strings.ReplaceAll(step.Damage, "/", "%2f"), filepath.FromSlash(step.Path))
						line = "damage: " + archiveClass(os.WriteFile(path, []byte("damaged"), 0o600))
					}
					logs[i] = append(logs[i], line)
				}
				s.Close()
				trees[i] = storeTree(t, root)
			}
			if !reflect.DeepEqual(logs[0], logs[1]) {
				t.Errorf("outcomes differ\n go: %s\n  c: %s", strings.Join(logs[0], "\n     "), strings.Join(logs[1], "\n     "))
			}
			if !reflect.DeepEqual(trees[0], trees[1]) {
				t.Errorf("stores differ\n go: %s\n  c: %s", strings.Join(trees[0], "\n     "), strings.Join(trees[1], "\n     "))
			}
		})
	}
}

// An archive that is not there, a URL and a closed store are refused the same way.
func TestInstallArchiveArguments(t *testing.T) {
	zeros := strings.Repeat("0", 64)
	for name, open := range map[string]func(string, PackageStoreLimits) (archiveStoreOps, error){
		"go": func(root string, l PackageStoreLimits) (archiveStoreOps, error) { return openGoPackageStore(root, l) },
		"c":  func(root string, l PackageStoreLimits) (archiveStoreOps, error) { return OpenPackageStore(root, l) },
	} {
		s, err := open(filepath.Join(t.TempDir(), "plugins"), PackageStoreLimits{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.InstallArchive(filepath.Join(t.TempDir(), "absent"), zeros, "", false); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: missing archive: %v", name, err)
		}
		for _, bad := range []string{"", "https://example.invalid/a.zip", "file:///tmp/a.zip"} {
			if _, err := s.InstallArchive(bad, zeros, "", false); !errors.Is(err, plugin.ErrInvalid) {
				t.Errorf("%s: %q: %v", name, bad, err)
			}
		}
		if _, err := s.Receipt("example/none"); !errors.Is(err, plugin.ErrNotFound) {
			t.Errorf("%s: receipt of nothing: %v", name, err)
		}
		s.Close()
	}
	if _, err := OpenPackageStore(t.TempDir(), PackageStoreLimits{MaxArchiveBytes: -1}); !errors.Is(err, plugin.ErrInvalid) {
		t.Errorf("negative archive limit: %v", err)
	}
}
