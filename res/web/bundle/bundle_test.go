package bundle

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

var shimURL = regexp.MustCompile(`/__ext/([0-9a-f]{32})/shim\.js`)

// page plays the part of a browser tab: it loads the entry, learns the token
// from the injected shim tag the way a real page would, and reports back.
type page struct {
	t      *testing.T
	base   string
	token  string
	client *http.Client
	html   string
	header http.Header
}

func load(t *testing.T, base string) *page {
	t.Helper()
	p := &page{t: t, base: strings.TrimSuffix(base, "/"), client: &http.Client{Timeout: 5 * time.Second}}
	response, err := p.client.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	p.html, p.header = string(data), response.Header
	match := shimURL.FindStringSubmatch(p.html)
	if match == nil {
		t.Fatalf("the page carries no shim tag:\n%s", p.html)
	}
	p.token = match[1]
	return p
}

func (p *page) post(name, body string) int {
	p.t.Helper()
	request, _ := http.NewRequest(http.MethodPost, p.base+"/__ext/"+p.token+"/"+name, strings.NewReader(body))
	request.Header.Set("Origin", p.base)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response, err := p.client.Do(request)
	if err != nil {
		p.t.Fatal(err)
	}
	response.Body.Close()
	return response.StatusCode
}

func writeBundle(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const indexHTML = `<!doctype html><html><head><title>t</title></head><body>hello</body></html>`

// run starts Run with an Open that hands the address to drive on its own goroutine.
func run(t *testing.T, options Options, drive func(base string)) (Result, error) {
	t.Helper()
	options.Open = func(_ context.Context, base string) error {
		if drive != nil {
			go drive(base)
		}
		return nil
	}
	return Run(context.Background(), options)
}

func TestAPageThatExitsEndsTheRun(t *testing.T) {
	root := writeBundle(t, map[string]string{"index.html": indexHTML})
	var mu sync.Mutex
	var events []Event
	result, err := run(t, Options{Root: root, Console: func(e Event) { mu.Lock(); events = append(events, e); mu.Unlock() }}, func(base string) {
		p := load(t, base)
		if !strings.Contains(p.html, "hello") || strings.Index(p.html, "shim.js") > strings.Index(p.html, "<title>") {
			t.Errorf("the shim must come first in <head> and leave the page intact:\n%s", p.html)
		}
		if csp := p.header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "connect-src 'self'") {
			t.Errorf("CSP %q", csp)
		}
		response, err := p.client.Get(p.base + "/__ext/" + p.token + "/shim.js")
		if err != nil {
			t.Error(err)
			return
		}
		script, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if !strings.Contains(string(script), "window.ext") || strings.Contains(string(script), "%TOKEN%") || !strings.Contains(string(script), p.token) {
			t.Error("the shim must be served with this run's token filled in")
		}
		p.post("log", `{"level":"warn","text":"careful"}`)
		p.post("log", `{"level":"nonsense","text":"x"}`)
		p.post("exit", `{"code":7}`)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusExited || result.ExitCode != 7 || !strings.HasPrefix(result.URL, "http://127.0.0.1:") {
		t.Fatalf("%+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 || events[0] != (Event{"warn", "careful"}) || events[1].Level != "log" {
		t.Fatalf("events %+v", events)
	}
}

func TestAJavaScriptEntryGetsAGeneratedPage(t *testing.T) {
	root := writeBundle(t, map[string]string{"app/main.mjs": `window.ext.exit(0)`})
	result, err := run(t, Options{Root: root, Entry: "app/main.mjs"}, func(base string) {
		p := load(t, base)
		if !strings.Contains(p.html, `type="module" src="/app/main.mjs"`) {
			t.Errorf("generated page:\n%s", p.html)
		}
		response, err := p.client.Get(p.base + "/app/main.mjs")
		if err != nil || response.StatusCode != 200 {
			t.Errorf("the entry script must be served: %v %v", err, response)
		}
		p.post("exit", `{}`)
	})
	if err != nil || result.Status != StatusExited || result.ExitCode != 0 {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestWebAssemblyIsServedWithItsContentType(t *testing.T) {
	root := writeBundle(t, map[string]string{"index.html": indexHTML, "m.wasm": "\x00asm\x01\x00\x00\x00"})
	_, err := run(t, Options{Root: root}, func(base string) {
		p := load(t, base)
		response, err := p.client.Get(p.base + "/m.wasm")
		if err != nil {
			t.Error(err)
		} else {
			if got := response.Header.Get("Content-Type"); got != "application/wasm" {
				t.Errorf("content type %q", got)
			}
			response.Body.Close()
		}
		p.post("exit", `{}`)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// rawGet sends the request target exactly as given, so a path such as
// "/../secret.txt" reaches the server uncleaned, as an attacker would send it.
func rawGet(t *testing.T, address, target string) (int, string) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(connection, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", target, address)
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatalf("%s: %v", target, err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(data)
}

func TestOnlyTheBundleIsReachable(t *testing.T) {
	parent := t.TempDir()
	if err := os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("outside-secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "bundle")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// secret.txt also exists inside the bundle, so a request that is merely
	// collapsed to it would be served and the test would notice.
	for name, content := range map[string]string{"index.html": indexHTML, ".env": "token=1", "sub/ok.txt": "ok", "secret.txt": "inside"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	linked := os.Symlink(filepath.Join(parent, "secret.txt"), filepath.Join(root, "link.txt")) == nil
	_, err := run(t, Options{Root: root}, func(base string) {
		p := load(t, base)
		address := strings.TrimPrefix(p.base, "http://")
		blocked := []string{"/../secret.txt", "/%2e%2e/secret.txt", "/%2E%2E/secret.txt", "/sub/../../secret.txt", "/sub/../secret.txt",
			"/..%2fsecret.txt", "/sub/..%2f..%2fsecret.txt", "/.env", "/%2eenv", "/sub/", "/sub", "/missing", "/sub/%00", "/./.env"}
		if linked {
			blocked = append(blocked, "/link.txt")
		}
		for _, target := range blocked {
			if code, body := rawGet(t, address, target); code == 200 || strings.Contains(body, "outside-secret") || strings.Contains(body, "inside") || strings.Contains(body, "token=1") {
				t.Errorf("%s was served: %d %q", target, code, body)
			}
		}
		if code, body := rawGet(t, address, "/sub/ok.txt"); code != 200 || body != "ok" {
			t.Errorf("a file inside the bundle must be served: %d %q", code, body)
		}
		if code, body := rawGet(t, address, "/secret.txt"); code != 200 || body != "inside" {
			t.Errorf("the bundle's own secret.txt is reachable by its real path: %d %q", code, body)
		}
		p.post("exit", `{}`)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOnlyTheRunsOwnPageCanReport(t *testing.T) {
	root := writeBundle(t, map[string]string{"index.html": indexHTML})
	result, err := run(t, Options{Root: root, Timeout: 3 * time.Second}, func(base string) {
		p := load(t, base)
		send := func(path string, headers map[string]string, host string, body string, method string) int {
			request, _ := http.NewRequest(method, p.base+path, strings.NewReader(body))
			if host != "" {
				request.Host = host
			}
			for key, value := range headers {
				request.Header.Set(key, value)
			}
			response, err := p.client.Do(request)
			if err != nil {
				t.Error(err)
				return 0
			}
			response.Body.Close()
			return response.StatusCode
		}
		exit := "/__ext/" + p.token + "/exit"
		wrongToken := "/__ext/" + strings.Repeat("0", 32) + "/exit"
		cases := []struct {
			name string
			code int
			got  int
		}{
			{"wrong token", 404, send(wrongToken, nil, "", `{"code":9}`, "POST")},
			{"no token", 404, send("/__ext/exit", nil, "", `{"code":9}`, "POST")},
			{"rebound host name", 421, send(exit, nil, "evil.example:80", `{"code":9}`, "POST")},
			{"another origin", 403, send(exit, map[string]string{"Origin": "https://evil.example"}, "", `{"code":9}`, "POST")},
			{"a cross-site request", 403, send(exit, map[string]string{"Sec-Fetch-Site": "cross-site"}, "", `{"code":9}`, "POST")},
			{"a GET report", 403, send(exit, nil, "", "", "GET")},
			{"an oversized body", 400, send("/__ext/"+p.token+"/log", map[string]string{"Origin": p.base}, "", strings.Repeat("x", maxBodyBytes+10), "POST")},
			{"malformed JSON", 400, send("/__ext/"+p.token+"/log", map[string]string{"Origin": p.base}, "", `{nope`, "POST")},
		}
		for _, c := range cases {
			if c.got != c.code {
				t.Errorf("%s: status %d, want %d", c.name, c.got, c.code)
			}
		}
		// None of the above may have ended the run; this one does.
		p.post("exit", `{"code":3}`)
	})
	if err != nil || result.Status != StatusExited || result.ExitCode != 3 {
		t.Fatalf("a rejected report must not end the run: %+v %v", result, err)
	}
}

func TestAClosedPageEndsTheRun(t *testing.T) {
	root := writeBundle(t, map[string]string{"index.html": indexHTML})
	result, err := run(t, Options{Root: root, Timeout: 5 * time.Second}, func(base string) {
		p := load(t, base)
		p.post("alive", `{}`)
		p.post("closed", `{}`)
	})
	if err != nil || result.Status != StatusClosed {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestASilentPageIsTreatedAsClosed(t *testing.T) {
	root := writeBundle(t, map[string]string{"index.html": indexHTML})
	started := time.Now()
	result, err := run(t, Options{Root: root, HeartbeatTimeout: 400 * time.Millisecond, Timeout: 10 * time.Second}, func(base string) {
		load(t, base).post("alive", `{}`) // reports once, then goes quiet
	})
	if err != nil || result.Status != StatusClosed || !strings.Contains(result.Reason, "stopped reporting") || time.Since(started) > 5*time.Second {
		t.Fatalf("%+v %v after %s", result, err, time.Since(started))
	}
}

func TestAPageThatNeverLoadsFails(t *testing.T) {
	root := writeBundle(t, map[string]string{"index.html": indexHTML})
	result, err := run(t, Options{Root: root, StartupTimeout: 300 * time.Millisecond}, nil)
	if err != nil || result.Status != StatusFailed || !strings.Contains(result.Reason, "did not load") {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestTheTimeLimitStopsALivePage(t *testing.T) {
	root := writeBundle(t, map[string]string{"index.html": indexHTML})
	stop := make(chan struct{})
	defer close(stop)
	started := time.Now()
	result, err := run(t, Options{Root: root, Timeout: 400 * time.Millisecond}, func(base string) {
		p := load(t, base)
		for {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				p.post("alive", `{}`)
			}
		}
	})
	if err != nil || result.Status != StatusTimeout || time.Since(started) > 5*time.Second {
		t.Fatalf("%+v %v after %s", result, err, time.Since(started))
	}
}

func TestACancelledRunReturnsTheContextError(t *testing.T) {
	root := writeBundle(t, map[string]string{"index.html": indexHTML})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	_, err := Run(ctx, Options{Root: root, Open: func(context.Context, string) error { return nil }})
	if err != context.Canceled {
		t.Fatalf("got %v", err)
	}
}

func TestAnOpenFailureIsAFailedResult(t *testing.T) {
	root := writeBundle(t, map[string]string{"index.html": indexHTML})
	result, err := Run(context.Background(), Options{Root: root, Open: func(context.Context, string) error { return os.ErrPermission }})
	if err != nil || result.Status != StatusFailed || !strings.Contains(result.Reason, "could not be opened") {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestRunRefusesAnUnusableConfiguration(t *testing.T) {
	root := writeBundle(t, map[string]string{"index.html": indexHTML, "dir/x.txt": "x"})
	open := func(context.Context, string) error { return nil }
	for name, options := range map[string]Options{
		"no Open":          {Root: root},
		"missing root":     {Root: filepath.Join(root, "absent"), Open: open},
		"root is a file":   {Root: filepath.Join(root, "index.html"), Open: open},
		"missing entry":    {Root: root, Entry: "nope.html", Open: open},
		"entry is a dir":   {Root: root, Entry: "dir", Open: open},
		"entry escapes":    {Root: root, Entry: "../x", Open: open},
		"entry is dotfile": {Root: root, Entry: ".hidden", Open: open},
	} {
		if _, err := Run(context.Background(), options); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPolicyRendersTheContentSecurityPolicy(t *testing.T) {
	strict := Policy{}.ContentSecurityPolicy()
	for _, want := range []string{"default-src 'none'", "connect-src 'self'", "frame-src 'none'", "form-action 'none'", "'wasm-unsafe-eval'"} {
		if !strings.Contains(strict, want) {
			t.Errorf("strict policy lacks %q: %s", want, strict)
		}
	}
	if strings.Contains(strict, "*") {
		t.Errorf("the strict policy must not allow the network: %s", strict)
	}
	if open := (Policy{AllowNet: true}).ContentSecurityPolicy(); !strings.Contains(open, "connect-src *") {
		t.Errorf("AllowNet: %s", open)
	}
	specific := Policy{AllowOrigins: []string{"https://api.example.com", "https://x.com; script-src *", "javascript:alert(1)", "https://a.com/path", "http://ok.test"}}.ContentSecurityPolicy()
	if !strings.Contains(specific, "connect-src 'self' https://api.example.com http://ok.test;") || strings.Contains(specific, "script-src *") || strings.Contains(specific, "javascript:") || strings.Contains(specific, "a.com") {
		t.Errorf("origins must be filtered to plain http(s) origins: %s", specific)
	}
}
