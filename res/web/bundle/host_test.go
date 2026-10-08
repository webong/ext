package bundle

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHostChannelCarriesMessagesBothWays(t *testing.T) {
	root := writeBundle(t, map[string]string{"index.html": indexHTML})
	var mu sync.Mutex
	var received []string
	var frame string
	ready := make(chan func(string) error, 1)
	result, err := run(t, Options{
		Root:      root,
		Timeout:   10 * time.Second,
		OnMessage: func(m string) { mu.Lock(); received = append(received, m); mu.Unlock() },
		OnReady:   func(send func(string) error) { ready <- send },
	}, func(base string) {
		p := load(t, base)
		p.post("alive", `{}`)
		// page to host, including characters JSON must escape and a plugin-shaped frame
		frame = `{"apiVersion":"ext.plugin/v1","id":"1","payload":{"t":"héllo 🙂 \"quoted\"\n"}}`
		if code := p.post("host", `{"data":`+quoteJSON(frame)+`}`); code != 204 {
			t.Errorf("host send status %d", code)
		}
		// host to page: the page asks, and gets what the host queued
		send := <-ready
		for _, message := range []string{"first", "second"} {
			if err := send(message); err != nil {
				t.Error(err)
			}
		}
		for _, want := range []string{"first", "second"} {
			response, err := p.client.Get(p.base + "/__ext/" + p.token + "/next")
			if err != nil {
				t.Error(err)
				continue
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != 200 || string(body) != want {
				t.Errorf("next: %d %q, want %q", response.StatusCode, body, want)
			}
		}
		p.post("exit", `{"code":0}`)
	})
	if err != nil || result.Status != StatusExited {
		t.Fatalf("%+v %v", result, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 || received[0] != frame {
		t.Fatalf("received %q", received)
	}
}

func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func TestAnIdlePollAnswersNothingYet(t *testing.T) {
	previous := pollWait
	pollWait = 150 * time.Millisecond
	defer func() { pollWait = previous }()
	root := writeBundle(t, map[string]string{"index.html": indexHTML})
	_, err := run(t, Options{Root: root, Timeout: 10 * time.Second}, func(base string) {
		p := load(t, base)
		started := time.Now()
		response, err := p.client.Get(p.base + "/__ext/" + p.token + "/next")
		if err != nil {
			t.Error(err)
		} else {
			response.Body.Close()
			if response.StatusCode != 204 || time.Since(started) < 100*time.Millisecond {
				t.Errorf("an empty poll: status %d after %s", response.StatusCode, time.Since(started))
			}
		}
		p.post("exit", `{}`)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHostChannelLimitsAndAccess(t *testing.T) {
	root := writeBundle(t, map[string]string{"index.html": indexHTML})
	ready := make(chan func(string) error, 1)
	_, err := run(t, Options{Root: root, Timeout: 10 * time.Second, OnReady: func(send func(string) error) { ready <- send }}, func(base string) {
		p := load(t, base)
		p.post("alive", `{}`)
		send := <-ready
		if err := send(strings.Repeat("x", maxMessageBytes+1)); err == nil {
			t.Error("an oversized message was queued")
		}
		for i := 0; i < queuedMessages; i++ {
			if err := send("m"); err != nil {
				t.Errorf("message %d: %v", i, err)
				break
			}
		}
		if err := send("overflow"); err == nil {
			t.Error("a full queue must refuse more")
		}
		next := func(headers map[string]string, token string) int {
			request, _ := http.NewRequest(http.MethodGet, p.base+"/__ext/"+token+"/next", nil)
			for k, v := range headers {
				request.Header.Set(k, v)
			}
			response, err := p.client.Do(request)
			if err != nil {
				t.Error(err)
				return 0
			}
			response.Body.Close()
			return response.StatusCode
		}
		if code := next(nil, strings.Repeat("0", 32)); code != 404 {
			t.Errorf("wrong token: %d", code)
		}
		if code := next(map[string]string{"Sec-Fetch-Site": "cross-site"}, p.token); code != 403 {
			t.Errorf("a cross-site poll must be refused: %d", code)
		}
		if code := p.post("host", `{nope`); code != 400 {
			t.Errorf("malformed host message: %d", code)
		}
		p.post("exit", `{}`)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSendAfterTheRunEndsFails(t *testing.T) {
	root := writeBundle(t, map[string]string{"index.html": indexHTML})
	ready := make(chan func(string) error, 1)
	_, err := run(t, Options{Root: root, Timeout: 10 * time.Second, OnReady: func(send func(string) error) { ready <- send }}, func(base string) {
		p := load(t, base)
		p.post("alive", `{}`)
		p.post("exit", `{}`)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := (<-ready)("late"); err == nil {
		t.Fatal("a message after the run ended must be refused")
	}
}

// TestShimTransports runs the real page script in Node against mock webview
// handlers, so the iOS and Android wiring is checked without a phone.
func TestShimTransports(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to run the page script")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shim.js"), []byte(Shim()), 0o644); err != nil {
		t.Fatal(err)
	}
	test := `
const fs = require("fs"), vm = require("vm"), assert = require("assert");
const shim = fs.readFileSync(__dirname + "/shim.js", "utf8");
function boot(extra, replace) {
  const posted = [], fetched = [], intervals = [];
  const window = Object.assign({ addEventListener() {}, close() {} }, extra(posted));
  const context = vm.createContext({
    window, console: { log() {}, info() {}, warn() {}, error() {}, debug() {} },
    setInterval: (fn, ms) => intervals.push(ms), setTimeout() {},
    fetch: (url, options) => { fetched.push({ url, options }); return Promise.resolve({ status: 204, text: () => Promise.resolve("") }); },
    Promise, JSON,
  });
  vm.runInContext(replace ? shim.replace("%TOKEN%", replace) : shim, context);
  return { window, posted, fetched };
}
const frames = (posted) => posted.map((s) => JSON.parse(s));

// iOS: the page reports through the "ext" message handler.
let ios = boot((posted) => ({ webkit: { messageHandlers: { ext: { postMessage: (s) => posted.push(s) } } } }));
assert.strictEqual(ios.window.ext.host.transport, "wkwebview");
ios.window.ext.host.send('{"apiVersion":"ext.plugin/v1"}');
ios.window.ext.log("a", { b: 1 });
ios.window.ext.exit(7);
const got = frames(ios.posted);
assert.deepStrictEqual(got.find((f) => f.kind === "host"), { data: '{"apiVersion":"ext.plugin/v1"}', kind: "host" });
assert.deepStrictEqual(got.find((f) => f.kind === "log" && f.level === "log"), { level: "log", text: 'a {"b":1}', kind: "log" });
assert.strictEqual(got.find((f) => f.kind === "exit").code, 7);
assert.ok(got.some((f) => f.kind === "alive"), "the page announces itself");
assert.strictEqual(ios.fetched.length, 0, "a native page never uses HTTP");
// host to page: native calls _deliver, the page receives it in onmessage
const seen = [];
ios.window.ext.host.onmessage = (m) => seen.push(m);
ios.window.ext.host._deliver('{"id":"1"}');
ios.window.ext.host._deliver(42);
assert.deepStrictEqual(seen, ['{"id":"1"}', "42"]);
assert.strictEqual(ios.fetched.length, 0, "a native page does not poll");

// Android: the same frames through the ExtHost interface.
const android = boot((posted) => ({ ExtHost: { postMessage: (s) => posted.push(s) } }));
assert.strictEqual(android.window.ext.host.transport, "android");
android.window.ext.host.send("hello");
assert.deepStrictEqual(frames(android.posted).find((f) => f.kind === "host"), { data: "hello", kind: "host" });

// Loopback HTTP: a served page has a token, posts to it and polls for messages.
const served = boot(() => ({}), "t".repeat(32));
assert.strictEqual(served.window.ext.host.transport, "http");
served.window.ext.host.send("over http");
let post = served.fetched.find((f) => f.url.endsWith("/host"));
assert.ok(post && post.url.startsWith("/__ext/" + "t".repeat(32) + "/") && JSON.parse(post.options.body).data === "over http");
served.window.ext.host.onmessage = () => {};
assert.ok(served.fetched.some((f) => f.url.endsWith("/next")), "setting onmessage starts polling");

// A page with neither a handler nor a token has no transport and must not throw.
const bare = boot(() => ({}));
assert.strictEqual(bare.window.ext.host.transport, "none");
bare.window.ext.host.send("dropped");
bare.window.ext.exit(0);
assert.strictEqual(bare.fetched.length, 0);
console.log("ok");
`
	if err := os.WriteFile(filepath.Join(dir, "test.js"), []byte(test), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, node, filepath.Join(dir, "test.js")).CombinedOutput()
	if err != nil || !strings.Contains(string(output), "ok") {
		t.Fatalf("%v\n%s", err, output)
	}
}

func TestTheShimKeepsItsPlaceholder(t *testing.T) {
	if !strings.Contains(Shim(), `"%TOKEN%"`) {
		t.Fatal("Shim must return the script with its token placeholder, for embedded hosts")
	}
}
