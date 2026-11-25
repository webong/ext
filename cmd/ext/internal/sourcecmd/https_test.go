package sourcecmd

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHTTPSFetchPolicy(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			io.WriteString(w, "index")
		case "/redirect":
			http.Redirect(w, r, "/ok", http.StatusFound)
		case "/cross":
			http.Redirect(w, r, "https://other.invalid/index", http.StatusFound)
		case "/query":
			http.Redirect(w, r, "/ok?token=secret", http.StatusFound)
		case "/downgrade":
			http.Redirect(w, r, "http://example.org/index", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/large":
			w.Header().Set("Content-Length", fmt.Sprint(maxIndexBytes+1))
			io.WriteString(w, strings.Repeat("x", maxIndexBytes+1))
		case "/chunked":
			w.(http.Flusher).Flush()
			io.WriteString(w, strings.Repeat("x", maxIndexBytes+1))
		default:
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	m := Manager{transport: server.Client().Transport}
	for _, path := range []string{"/ok", "/redirect"} {
		data, err := m.fetch(server.URL + path)
		if err != nil || string(data) != "index" {
			t.Fatalf("%s: %s %v", path, data, err)
		}
	}
	for _, path := range []string{"/cross", "/query", "/downgrade", "/loop", "/large", "/chunked", "/failure"} {
		if _, err := m.fetch(server.URL + path); err == nil {
			t.Errorf("accepted %s", path)
		}
	}
	// The production client must reject a certificate absent from system roots.
	if _, err := (Manager{}).fetch(server.URL + "/ok"); err == nil {
		t.Fatal("accepted untrusted TLS certificate")
	}
}

func TestHTTPSRefreshPreservesSnapshot(t *testing.T) {
	lib := os.Getenv("EXT_ACCEPTANCE_LIBRARY")
	if lib == "" {
		t.Skip("run scripts/package-acceptance.sh for C catalog acceptance")
	}
	t.Setenv("EXT_PLUGIN_LIBRARY", lib)
	var mode atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, pin := "example/tool", "a"
		switch mode.Load() {
		case 1:
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		case 2:
			io.WriteString(w, "{invalid")
			return
		case 3:
			name = "example/other"
		case 4:
			pin = "b"
		}
		fmt.Fprintf(w, `{"format_version":1,"name":%q,"packages":{"linux/amd64":{"url":"tool.zip","sha256":%q}}}`, name, strings.Repeat(pin, 64))
	}))
	defer server.Close()
	m := Manager{Home: t.TempDir(), transport: server.Client().Transport}
	if err := m.run("add", []string{"tools", server.URL + "/index"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.Home, "sources.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []int32{1, 2, 3} {
		mode.Store(value)
		if err := m.run("refresh", []string{"tools"}, io.Discard); err == nil {
			t.Fatalf("accepted refresh mode %d", value)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("failed refresh changed snapshot: %v", err)
		}
	}
	mode.Store(4)
	if err := m.run("refresh", []string{"tools"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || bytes.Equal(before, after) {
		t.Fatalf("successful refresh did not update snapshot: %v", err)
	}
}
