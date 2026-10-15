package chromium

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	management "github.com/webong/ext/res/web/contract"
)

const endpointGUID = "0f3c2b1a-9d4e-4a77-8c1d-5e6f7a8b9c0d"

func shortProbe(t *testing.T) {
	t.Helper()
	old := EndpointProbeTimeout
	EndpointProbeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { EndpointProbeTimeout = old })
}

// endpointProfile writes a debugging marker into a fresh user data directory.
func endpointProfile(t *testing.T, marker string) string {
	t.Helper()
	root := t.TempDir()
	if marker != "" {
		if err := os.WriteFile(filepath.Join(root, "DevToolsActivePort"), []byte(marker), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// listenOnce accepts connections and reports any bytes the client sends.
func listenOnce(t *testing.T) (port int, received chan []byte, closeListener func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	received = make(chan []byte, 4)
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				connection.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
				buffer := make([]byte, 64)
				n, _ := connection.Read(buffer)
				received <- buffer[:n]
			}()
		}
	}()
	t.Cleanup(func() { listener.Close() })
	return listener.Addr().(*net.TCPAddr).Port, received, func() { listener.Close() }
}

func endpointBackendFor() extensionBackend { return extensionBackend{config: exampleConfig()} }

func TestEndpointReportsTheLiveBrowserEndpoint(t *testing.T) {
	shortProbe(t)
	port, received, _ := listenOnce(t)
	profile := endpointProfile(t, fmt.Sprintf("%d\n/devtools/browser/%s\n", port, endpointGUID))
	result, err := endpointBackendFor().Endpoint(context.Background(), profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("ws://127.0.0.1:%d/devtools/browser/%s", port, endpointGUID)
	if result.Protocol != "cdp" || result.Endpoint != want {
		t.Fatalf("unexpected result: %v", result)
	}
	if err := management.ValidateEndpoint(result); err != nil {
		t.Fatal(err)
	}
	// The probe only connects and closes: it sends nothing.
	select {
	case data := <-received:
		if len(data) != 0 {
			t.Fatalf("the probe sent data: %q", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the probe never connected")
	}
	// An empty or object input is accepted; anything else is not.
	for _, raw := range []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(` `)} {
		if _, err := endpointBackendFor().Endpoint(context.Background(), profile, raw); err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
	}
	for _, raw := range []string{`{"endpoint":"ws://127.0.0.1:1/x"}`, `[1]`, `"x"`} {
		if _, err := endpointBackendFor().Endpoint(context.Background(), profile, json.RawMessage(raw)); err == nil {
			t.Fatalf("input %s accepted", raw)
		}
	}
}

func TestEndpointRefusesStaleOrBrokenMarkers(t *testing.T) {
	shortProbe(t)
	// A marker whose port nobody listens on is stale.
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stalePort := closed.Addr().(*net.TCPAddr).Port
	closed.Close()
	livePort, _, _ := listenOnce(t)
	cases := map[string]string{
		"stale marker":      fmt.Sprintf("%d\n/devtools/browser/%s\n", stalePort, endpointGUID),
		"no marker":         "",
		"wrong path prefix": fmt.Sprintf("%d\n/devtools/page/%s\n", livePort, endpointGUID),
		"one line":          fmt.Sprintf("%d\n", livePort),
		"three lines":       fmt.Sprintf("%d\n/devtools/browser/%s\nextra\n", livePort, endpointGUID),
		"port zero":         "0\n/devtools/browser/" + endpointGUID + "\n",
		"port too large":    "70000\n/devtools/browser/" + endpointGUID + "\n",
		"port not a number": "abc\n/devtools/browser/" + endpointGUID + "\n",
		"oversize marker":   strings.Repeat("9", 5000) + "\n/devtools/browser/" + endpointGUID + "\n",
	}
	for name, marker := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := endpointBackendFor().Endpoint(context.Background(), endpointProfile(t, marker), nil)
			if err == nil {
				t.Fatal("accepted")
			}
			if strings.Contains(err.Error(), endpointGUID) || strings.Contains(err.Error(), "/devtools/browser/") {
				t.Fatalf("error leaks the endpoint: %v", err)
			}
		})
	}
}

func TestEndpointNeedsACDPRouteAndAProfile(t *testing.T) {
	profile := endpointProfile(t, "")
	noRoute := extensionBackend{config: Config{Name: "example"}}
	if _, err := noRoute.Endpoint(context.Background(), profile, nil); err == nil {
		t.Fatal("adapter without a CDP page route answered")
	}
	if _, err := endpointBackendFor().Endpoint(context.Background(), "", nil); err == nil {
		t.Fatal("empty profile accepted")
	}
	if _, err := endpointBackendFor().Endpoint(context.Background(), "../escape", nil); err == nil {
		t.Fatal("relative escaping profile accepted")
	}
}

func TestEndpointProbeHonorsContextAndTimeout(t *testing.T) {
	shortProbe(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	port, _, _ := listenOnce(t)
	profile := endpointProfile(t, fmt.Sprintf("%d\n/devtools/browser/%s\n", port, endpointGUID))
	if _, err := endpointBackendFor().Endpoint(cancelled, profile, nil); err == nil {
		t.Fatal("cancelled context accepted")
	}
	if EndpointProbeTimeout != 300*time.Millisecond {
		t.Fatal("probe timeout must stay a variable tests can shorten")
	}
}

func TestEndpointOnlyReportsLoopback(t *testing.T) {
	shortProbe(t)
	port, _, _ := listenOnce(t)
	profile := endpointProfile(t, fmt.Sprintf("%d\n/devtools/browser/%s\n", port, endpointGUID))
	result, err := endpointBackendFor().Endpoint(context.Background(), profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Endpoint, "ws://127.0.0.1:") {
		t.Fatalf("the engine must report only 127.0.0.1: %v", result)
	}
}
