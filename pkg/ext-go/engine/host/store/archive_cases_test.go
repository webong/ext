package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
)

// The archive and download fixtures every store runs, in every language:
// lib/plugin/testdata/v1/archive-cases.json with the archives beside it, and
// download-cases.json. They are outside this module, so the tests skip when the
// module is used without the repository.
const archiveFixtureDir = "../../../../../lib/plugin/testdata/v1"

type archiveCases struct {
	About string        `json:"about"`
	Cases []archiveCase `json:"cases"`
}

type archiveCase struct {
	Name   string        `json:"name"`
	Limits *caseLimits   `json:"limits,omitempty"`
	Steps  []archiveStep `json:"steps"`
}

type caseLimits struct {
	MaxArchiveBytes  int64 `json:"maxArchiveBytes,omitempty"`
	MaxTreeFiles     int   `json:"maxTreeFiles,omitempty"`
	MaxTreeBytes     int64 `json:"maxTreeBytes,omitempty"`
	MaxArtifactBytes int64 `json:"maxArtifactBytes,omitempty"`
}

type archiveStep struct {
	Install  string  `json:"install,omitempty"`
	Receipt  string  `json:"receipt,omitempty"`
	Remove   string  `json:"remove,omitempty"`
	Damage   string  `json:"damage,omitempty"`
	Path     string  `json:"path,omitempty"`
	SHA256   *string `json:"sha256,omitempty"`
	ID       string  `json:"id,omitempty"`
	Status   string  `json:"status,omitempty"`
	Revision string  `json:"revision,omitempty"`
}

func fixtureStatus(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, store.ErrExists):
		return "exists"
	case errors.Is(err, store.ErrNotFound), errors.Is(err, plugin.ErrNotFound):
		return "not_found"
	case errors.Is(err, plugin.ErrMismatch):
		return "mismatch"
	case errors.Is(err, plugin.ErrUnsupported):
		return "unsupported"
	case errors.Is(err, plugin.ErrInvalid):
		return "invalid"
	}
	return "failed"
}

func readFixture(t *testing.T, name string, into any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(archiveFixtureDir, name))
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("needs the repository's lib/plugin/testdata")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveCases(t *testing.T) {
	var fixture archiveCases
	readFixture(t, "archive-cases.json", &fixture)
	if len(fixture.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var limits store.Limits
			if c.Limits != nil {
				limits = store.Limits{MaxArchiveBytes: c.Limits.MaxArchiveBytes, MaxTreeFiles: c.Limits.MaxTreeFiles,
					MaxTreeBytes: c.Limits.MaxTreeBytes, MaxArtifactBytes: c.Limits.MaxArtifactBytes}
			}
			s, err := store.New(filepath.Join(t.TempDir(), "plugins"), limits)
			if err != nil {
				t.Fatal(err)
			}
			for i, step := range c.Steps {
				var got, revision, digest string
				switch {
				case step.Install != "":
					archive := store.Archive{Source: filepath.Join(archiveFixtureDir, filepath.FromSlash(step.Install)), ID: step.ID}
					if step.SHA256 != nil {
						archive.SHA256 = *step.SHA256
					}
					installed, err := s.InstallArchive(context.Background(), archive, store.ArchiveOptions{})
					got, revision = fixtureStatus(err), installed.Manifest.Descriptor.Identity.Revision
				case step.Receipt != "":
					receipt, err := s.Receipt(step.Receipt)
					got, revision, digest = fixtureStatus(err), receipt.Revision, receipt.SHA256
					if err == nil && (step.SHA256 == nil || digest != *step.SHA256) {
						t.Fatalf("step %d: receipt names archive %s", i, digest)
					}
				case step.Remove != "":
					got = fixtureStatus(s.Remove(step.Remove))
				case step.Damage != "":
					path := filepath.Join(s.Root(), strings.ReplaceAll(step.Damage, "/", "%2f"), filepath.FromSlash(step.Path))
					got = fixtureStatus(os.WriteFile(path, []byte("damaged"), 0o600))
				default:
					t.Fatalf("step %d: nothing to do", i)
				}
				want := step.Status
				if want == "" {
					want = "ok"
				}
				if got != want || revision != step.Revision {
					t.Fatalf("step %d: %s revision %q, want %s revision %q", i, got, revision, want, step.Revision)
				}
			}
			// Whatever happened, nothing is left under the root but packages and receipts.
			entries, _ := os.ReadDir(s.Root())
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".") && entry.Name() != ".receipts" {
					t.Fatalf("left %s in the store", entry.Name())
				}
			}
			if _, err := os.Lstat("/tmp/ext-archive-escape"); err == nil {
				t.Fatal("an archive wrote outside the store")
			}
		})
	}
}

type downloadCases struct {
	About        string                   `json:"about"`
	MaxRedirects int                      `json:"maxRedirects"`
	Routes       map[string]downloadRoute `json:"routes"`
	Cases        []downloadCase           `json:"cases"`
}

type downloadRoute struct {
	Status    int    `json:"status,omitempty"`
	Body      string `json:"body,omitempty"`
	BodyBytes int    `json:"bodyBytes,omitempty"`
	Chunked   bool   `json:"chunked,omitempty"`
	Redirect  string `json:"redirect,omitempty"`
}

type downloadCase struct {
	Name              string            `json:"name"`
	URL               string            `json:"url"`
	MaxBytes          int64             `json:"maxBytes,omitempty"`
	Headers           map[string]string `json:"headers,omitempty"`
	DestinationExists bool              `json:"destinationExists,omitempty"`
	Expect            string            `json:"expect"`
	Body              string            `json:"body,omitempty"`
	AbsentAtLanding   []string          `json:"absentAtLanding,omitempty"`
}

func TestDownloadCases(t *testing.T) {
	var fixture downloadCases
	readFixture(t, "download-cases.json", &fixture)
	if fixture.MaxRedirects != store.MaxDownloadRedirects || len(fixture.Cases) == 0 {
		t.Fatalf("fixture allows %d redirects, the store %d", fixture.MaxRedirects, store.MaxDownloadRedirects)
	}
	var mu sync.Mutex
	var landed http.Header
	plainHits := 0
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		plainHits++
		mu.Unlock()
	}))
	defer plain.Close()
	var fill func(string) string
	server, client := origin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, ok := fixture.Routes[r.URL.Path]
		if !ok {
			t.Errorf("no route %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		landed = r.Header.Clone()
		mu.Unlock()
		if route.Redirect != "" {
			w.Header().Set("Location", fill(route.Redirect))
			w.WriteHeader(http.StatusFound)
			return
		}
		body := []byte(route.Body)
		if route.BodyBytes > 0 {
			body = bytes.Repeat([]byte{'x'}, route.BodyBytes)
		}
		status := route.Status
		if status == 0 {
			status = http.StatusOK
		}
		if route.Chunked {
			w.WriteHeader(status)
			w.(http.Flusher).Flush()
		} else {
			w.Header().Set("Content-Length", itoa(len(body)))
			w.WriteHeader(status)
		}
		_, _ = w.Write(body)
	}))
	fill = func(value string) string {
		value = strings.ReplaceAll(value, "{origin}", strings.TrimPrefix(server.URL, "https://"))
		return strings.ReplaceAll(value, "{plain}", strings.TrimPrefix(plain.URL, "http://"))
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "out")
			if c.DestinationExists {
				if err := os.WriteFile(destination, []byte("mine"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var doing store.HTTPClient = client
			if len(c.Headers) > 0 {
				doing = doer(func(request *http.Request) (*http.Response, error) {
					for name, value := range c.Headers {
						request.Header.Set(name, value)
					}
					return client.Do(request)
				})
			}
			err := store.Download(context.Background(), doing, fill(c.URL), destination, c.MaxBytes)
			got := fixtureStatus(err)
			if got != c.Expect {
				t.Fatalf("got %s (%v), want %s", got, err, c.Expect)
			}
			data, readErr := os.ReadFile(destination)
			switch {
			case c.DestinationExists:
				if string(data) != "mine" {
					t.Fatal("the existing file was changed")
				}
			case c.Expect == "ok":
				if readErr != nil || string(data) != c.Body {
					t.Fatalf("downloaded %q: %v", data, readErr)
				}
			case !errors.Is(readErr, os.ErrNotExist):
				t.Fatal("a failed download left a file")
			}
			mu.Lock()
			defer mu.Unlock()
			for _, name := range c.AbsentAtLanding {
				if value := landed.Get(name); value != "" {
					t.Errorf("%s followed the redirect: %q", name, value)
				}
			}
			if plainHits != 0 {
				t.Fatal("a request went to the plain HTTP server")
			}
		})
	}
}

func itoa(n int) string {
	data, _ := json.Marshal(n)
	return string(data)
}
