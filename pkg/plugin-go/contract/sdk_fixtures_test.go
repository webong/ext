package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Language-neutral fixtures for the SDKs that implement the guest side of progress
// events and reverse calls. This Go contract is their oracle: every outcome below is
// computed here, and every SDK must reproduce it (as for endpoint-cases.json). The
// test checks the committed files against what the contract computes now, so a file
// cannot go stale; EXT_UPDATE_FIXTURES=1 rewrites them.
//
//	frame-cases.json            progress frames, reverse requests, reverse responses
//	progress-limiter-cases.json the sender's token bucket, traced against a clock
//	model-cases.json            descriptors that declare progress and reverse, and
//	                            requests that carry a progress rate
const fixtureDir = "../../../lib/plugin/testdata/v1"

func TestSDKFixtures(t *testing.T) {
	for name, build := range map[string]func() []byte{
		"frame-cases.json":            buildFrameCases,
		"progress-limiter-cases.json": buildLimiterCases,
		"model-cases.json":            buildModelCases,
	} {
		t.Run(name, func(t *testing.T) {
			want := build()
			path := filepath.Join(fixtureDir, name)
			if os.Getenv("EXT_UPDATE_FIXTURES") == "1" {
				if err := os.WriteFile(path, want, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%s is stale: run EXT_UPDATE_FIXTURES=1 go test -run TestSDKFixtures ./pkg/plugin-go/contract", name)
			}
		})
	}
}

// ---- output: one case per line, as endpoint-cases.json is written ----

func compact(v any) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		panic(err)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

type section struct {
	key   string
	cases []any
}

func render(description string, sections []section, head ...string) []byte {
	var b strings.Builder
	b.WriteString("{\n  \"description\": " + compact(description))
	for _, h := range head {
		b.WriteString(",\n  " + h)
	}
	for _, s := range sections {
		b.WriteString(",\n  " + compact(s.key) + ": [\n")
		for i, c := range s.cases {
			b.WriteString("    " + compact(c))
			if i < len(s.cases)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString("  ]")
	}
	b.WriteString("\n}\n")
	return []byte(b.String())
}

// ---- mutations of a JSON document ----

type removed struct{}

func edgeValues() []any {
	return []any{nil, true, false, 0, -1, 1, 1.5, "", "x", "A", "ext.plugin/v2", []any{}, map[string]any{},
		strings.Repeat("a", 300), "é", json.Number("1048576"), json.Number("1048577"), json.Number("18446744073709551616")}
}

func deepCopy(v any) any {
	raw, _ := json.Marshal(v)
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var out any
	_ = d.Decode(&out)
	return out
}

// mutations returns the document with each member removed or replaced by each edge
// value, and each object given an unknown member, in a fixed order.
func mutations(doc any) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v any) {
		raw, err := json.Marshal(v)
		if err != nil {
			return
		}
		if s := string(raw); !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	var walk func(path []any)
	at := func(root any, path []any) (parent any, key any) {
		cur := root
		for _, p := range path[:len(path)-1] {
			switch c := cur.(type) {
			case map[string]any:
				cur = c[p.(string)]
			case []any:
				cur = c[p.(int)]
			}
		}
		return cur, path[len(path)-1]
	}
	value := func(path []any) any {
		parent, key := at(doc, path)
		switch p := parent.(type) {
		case map[string]any:
			return p[key.(string)]
		case []any:
			return p[key.(int)]
		}
		return nil
	}
	mutate := func(path []any, replacement any) {
		clone := deepCopy(doc)
		parent, key := at(clone, path)
		switch p := parent.(type) {
		case map[string]any:
			if _, drop := replacement.(removed); drop {
				delete(p, key.(string))
			} else {
				p[key.(string)] = replacement
			}
		case []any:
			i := key.(int)
			if _, drop := replacement.(removed); drop {
				p = append(p[:i], p[i+1:]...)
				// re-attach the shortened slice
				pp, pk := at(clone, path[:len(path)-1])
				switch q := pp.(type) {
				case map[string]any:
					q[pk.(string)] = p
				case []any:
					q[pk.(int)] = p
				}
			} else {
				p[i] = replacement
			}
		}
		add(clone)
	}
	walk = func(path []any) {
		var node any
		if len(path) == 0 {
			node = doc
		} else {
			node = value(path)
		}
		switch n := node.(type) {
		case map[string]any:
			keys := make([]string, 0, len(n))
			for k := range n {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			// an unknown member in this object
			clone := deepCopy(doc)
			target := clone
			for _, p := range path {
				switch c := target.(type) {
				case map[string]any:
					target = c[p.(string)]
				case []any:
					target = c[p.(int)]
				}
			}
			target.(map[string]any)["extra"] = json.Number("1")
			add(clone)
			for _, k := range keys {
				child := append(append([]any{}, path...), k)
				mutate(child, removed{})
				for _, v := range edgeValues() {
					mutate(child, v)
				}
				walk(child)
			}
		case []any:
			for i := range n {
				child := append(append([]any{}, path...), i)
				mutate(child, removed{})
				for _, v := range edgeValues() {
					mutate(child, v)
				}
				walk(child)
			}
		}
	}
	walk(nil)
	return out
}

func parse(s string) any {
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		panic(err)
	}
	return v
}

// ---- frame-cases.json ----

type frameCase struct {
	Name  string `json:"name"`
	Frame string `json:"frame"`
	ID    string `json:"id,omitempty"`
	Valid bool   `json:"valid"`
}

func judgeProgress(raw string) bool {
	var f ProgressFrame
	return Decode([]byte(raw), &f) == nil && f.Validate() == nil
}

func judgeReverseRequest(raw string) bool {
	var f ReverseRequestFrame
	return Decode([]byte(raw), &f) == nil && f.Validate() == nil
}

// A guest accepts a reverse response only for the call it is waiting on.
func judgeReverseResponse(raw, id string) bool {
	var f ReverseResponseFrame
	return Decode([]byte(raw), &f) == nil && f.Validate(id) == nil
}

func buildFrameCases() []byte {
	raws := func(name string, base map[string]any, extra []string, judge func(string) bool) []any {
		var cases []any
		n := 0
		add := func(label, raw string) {
			cases = append(cases, frameCase{Name: label, Frame: raw, Valid: judge(raw)})
		}
		add(name+": the base frame", compact(base))
		for _, raw := range extra {
			n++
			add(fmt.Sprintf("%s: hand-made %d", name, n), raw)
		}
		for i, raw := range mutations(base) {
			add(fmt.Sprintf("%s: mutation %d", name, i+1), raw)
		}
		return cases
	}
	progressBase := map[string]any{"frame": "progress", "apiVersion": "ext.plugin/v1", "parent": "1", "kind": "log", "stream": "out", "text": "hello", "completed": 1, "total": 3, "dropped": 2}
	progress := raws("progress", progressBase, []string{
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"progress","completed":1,"total":3}`,
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"log","dropped":7}`,
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"call-1","kind":"log","stream":"err","text":"oops"}`,
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"log","text":"` + strings.Repeat("x", 2048) + `"}`,
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"log","text":"` + strings.Repeat("x", 2049) + `"}`,
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"log","text":"` + strings.Repeat("é", 1024) + `"}`,
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"log","text":"` + strings.Repeat("é", 1025) + `"}`,
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"log","text":"a\u0000b"}`,
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"log","text":"\ud800"}`,
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"progress","completed":5,"total":2}`,
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"progress","completed":5}`,
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"progress","completed":-1}`,
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"progress","completed":1.0}`,
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"progress","completed":18446744073709551615,"total":18446744073709551615}`,
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"progress","completed":18446744073709551616}`,
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"log","kind":"log"}`,
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"log"} `,
		`{"frame":"progress","apiVersion":"ext.plugin/v1","parent":"1","kind":"log"} x`,
		`[]`, `null`, `"progress"`, `7`, ``, `{`,
	}, judgeProgress)
	requestBase := map[string]any{"frame": "reverse_request", "apiVersion": "ext.plugin/v1", "id": "g1", "parent": "1",
		"contract": map[string]any{"name": "host.svc", "version": "v1"}, "operation": "sign", "deadline": "2099-01-01T00:00:00Z", "payload": map[string]any{"n": 1}}
	sessionBase := map[string]any{"frame": "reverse_request", "apiVersion": "ext.plugin/v1", "id": "g2",
		"contract": map[string]any{"name": "host.svc", "version": "v1"}, "operation": "sign", "deadline": "2099-01-01T00:00:00.5+01:00"}
	request := raws("reverse request", requestBase, []string{
		`{"frame":"reverse_request","apiVersion":"ext.plugin/v1","id":"g1","parent":"","contract":{"name":"host.svc","version":"v1"},"operation":"sign","deadline":"2099-01-01T00:00:00Z"}`,
		`{"frame":"reverse_request","apiVersion":"ext.plugin/v1","id":"g1","parent":null,"contract":{"name":"host.svc","version":"v1"},"operation":"sign","deadline":"2099-01-01T00:00:00Z"}`,
		`{"frame":"reverse_request","apiVersion":"ext.plugin/v1","id":"g1","contract":{"name":"host.svc","version":"v1"},"operation":"sign","deadline":"2099-01-01T00:00:00Z","payload":null}`,
		`{"frame":"reverse_request","apiVersion":"ext.plugin/v1","id":"g1","contract":{"name":"host.svc","version":"v1"},"operation":"sign","deadline":"0001-01-01T00:00:00Z"}`,
		`{"frame":"reverse_request","apiVersion":"ext.plugin/v1","id":"g1","contract":{"name":"host.svc","version":"v1"},"operation":"sign","deadline":"2099-01-01T00:00:00"}`,
		`{"frame":"reverse_request","apiVersion":"ext.plugin/v1","id":"g1","contract":{"name":"host.svc","version":"v1"},"operation":"Sign","deadline":"2099-01-01T00:00:00Z"}`,
		`{"frame":"reverse_request","apiVersion":"ext.plugin/v1","id":"g1","contract":{"name":"host.svc","version":"v1"},"operation":"sign","operation":"sign","deadline":"2099-01-01T00:00:00Z"}`,
		`{"frame":"reverse_request","apiVersion":"ext.plugin/v1","id":"!!","contract":{"name":"host.svc","version":"v1"},"operation":"sign","deadline":"2099-01-01T00:00:00Z"}`,
		`[]`, `null`, ``,
	}, judgeReverseRequest)
	session := raws("session-scoped reverse request", sessionBase, nil, judgeReverseRequest)
	// A response is judged against the id of the call that waits for it.
	responses := []any{}
	respond := func(label, id, raw string) {
		responses = append(responses, frameCase{Name: label, Frame: raw, ID: id, Valid: judgeReverseResponse(raw, id)})
	}
	for _, c := range []struct{ label, id, raw string }{
		{"payload", "g1", `{"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","payload":"signed"}`},
		{"null payload", "g1", `{"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","payload":null}`},
		{"no payload", "g1", `{"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1"}`},
		{"object payload", "g1", `{"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","payload":{"n":1.10,"big":12345678901234567890}}`},
		{"public error", "g1", `{"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","error":{"code":"policy","message":"refused","retryAfterMilliseconds":5}}`},
		{"refusal", "g1", `{"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","error":{"code":"denied","message":"reverse call refused"}}`},
		{"error with payload", "g1", `{"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","payload":1,"error":{"code":"denied","message":"m"}}`},
		{"error with bad code", "g1", `{"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","error":{"code":"Denied","message":"m"}}`},
		{"error with long message", "g1", `{"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","error":{"code":"denied","message":"` + strings.Repeat("a", 4097) + `"}}`},
		{"retry hint of a year", "g1", `{"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","error":{"code":"busy","message":"m","retryAfterMilliseconds":31536000000}}`},
		{"retry hint over a year", "g1", `{"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","error":{"code":"busy","message":"m","retryAfterMilliseconds":31536000001}}`},
		{"another call's id", "g2", `{"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","payload":1}`},
		{"wrong tag", "g1", `{"frame":"progress","apiVersion":"ext.plugin/v1","id":"g1","payload":1}`},
		{"wrong version", "g1", `{"frame":"reverse_response","apiVersion":"ext.plugin/v2","id":"g1","payload":1}`},
		{"unknown field", "g1", `{"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","payload":1,"extra":1}`},
		{"duplicate key", "g1", `{"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","id":"g1","payload":1}`},
		{"not an object", "g1", `[]`},
		{"empty", "g1", ``},
	} {
		respond(c.label, c.id, c.raw)
	}
	for _, c := range []struct {
		label string
		raw   string
	}{
		{"payload with deep nesting", `{"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","payload":` + strings.Repeat("[", 70) + strings.Repeat("]", 70) + `}`},
		{"payload with deep nesting within the bound", `{"frame":"reverse_response","apiVersion":"ext.plugin/v1","id":"g1","payload":` + strings.Repeat("[", 60) + strings.Repeat("]", 60) + `}`},
	} {
		respond(c.label, "g1", c.raw)
	}
	return render("Frames of the JSON-line protocol, as a guest sees and sends them. 'frame' is the JSON text of one line; 'valid' is whether the Go contract accepts it as the section's frame type (strict decoding, then the frame's own rules). A reverse response is judged against the id of the call that waits for it ('id'). A binding passes when it reproduces every outcome. Regenerate with EXT_UPDATE_FIXTURES=1 go test -run TestSDKFixtures ./pkg/plugin-go/contract.",
		[]section{{"progress", progress}, {"reverseRequest", append(request, session...)}, {"reverseResponse", responses}})
}

// ---- progress-limiter-cases.json ----

type limiterStep struct {
	AtMs      int64  `json:"atMs"`
	TextBytes int    `json:"textBytes"`
	Admitted  bool   `json:"admitted"`
	Dropped   uint64 `json:"dropped,omitempty"`
}

type limiterCase struct {
	Name            string        `json:"name"`
	EventsPerSecond int           `json:"eventsPerSecond"`
	BytesPerSecond  int           `json:"bytesPerSecond"`
	Steps           []limiterStep `json:"steps"`
	Pending         uint64        `json:"pending"`
}

func buildLimiterCases() []byte {
	run := func(name string, events, bytes int, steps []limiterStep) limiterCase {
		t0 := time.Unix(1_800_000_000, 0)
		var now time.Time
		l := NewLimiter(ProgressLimits{EventsPerSecond: events, BytesPerSecond: bytes}, func() time.Time { return now })
		for i := range steps {
			now = t0.Add(time.Duration(steps[i].AtMs) * time.Millisecond)
			dropped, ok := l.Admit(Progress{Kind: "log", Text: strings.Repeat("x", steps[i].TextBytes)})
			steps[i].Admitted, steps[i].Dropped = ok, dropped
		}
		return limiterCase{Name: name, EventsPerSecond: events, BytesPerSecond: bytes, Steps: steps, Pending: l.Pending()}
	}
	burst := func(n int, at int64, size int) []limiterStep {
		var out []limiterStep
		for i := 0; i < n; i++ {
			out = append(out, limiterStep{AtMs: at, TextBytes: size})
		}
		return out
	}
	join := func(parts ...[]limiterStep) []limiterStep {
		var out []limiterStep
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	var cases []any
	add := func(c limiterCase) { cases = append(cases, c) }
	add(run("a burst of twice the rate passes, the rest is dropped and counted", 5, 1<<20, burst(14, 0, 1)))
	add(run("the next event reports the drops", 5, 1<<20, join(burst(12, 0, 1), burst(1, 1000, 1))))
	add(run("tokens refill with time, up to twice the rate", 5, 1<<20, join(burst(10, 0, 1), burst(3, 400, 1), burst(3, 5000, 1), burst(12, 5000, 1))))
	add(run("a fraction of a token is not enough", 5, 1<<20, join(burst(10, 0, 1), burst(1, 100, 1), burst(1, 200, 1), burst(1, 300, 1))))
	add(run("the byte rate limits as the event rate does", 1<<20, 10, join(burst(1, 0, 10), burst(1, 0, 10), burst(1, 0, 1), burst(1, 1000, 10), burst(1, 1000, 10))))
	add(run("an event larger than the byte burst is never admitted", 50, 10, burst(3, 0, 21)))
	add(run("an event as large as the byte burst is admitted once", 50, 10, join(burst(2, 0, 10), burst(1, 0, 1))))
	add(run("empty text costs an event and no bytes", 3, 1, burst(8, 0, 0)))
	add(run("zero limits use the defaults, 50 events and 64 KiB", 0, 0, join(burst(101, 0, 1), burst(1, 1000, 1))))
	add(run("zero events and an explicit byte rate", 0, 100, join(burst(3, 0, 60), burst(1, 600, 60))))
	add(run("negative limits use the defaults", -7, -7, burst(101, 0, 1)))
	add(run("time that does not advance adds nothing", 2, 1<<20, join(burst(4, 500, 1), burst(2, 500, 1))))
	add(run("time that runs backwards adds nothing", 2, 1<<20, join(burst(4, 1000, 1), burst(2, 0, 1), burst(1, 1000, 1))))
	add(run("a long idle period refills no more than the burst", 10, 1<<20, join(burst(20, 0, 1), burst(21, 3_600_000, 1))))
	add(run("drops still pending at the end are reported by a marker", 1, 1<<20, burst(5, 0, 1)))
	add(run("nothing pending when every event passed", 50, 1<<20, burst(10, 0, 100)))
	add(run("a steady rate under the limit never drops", 10, 1<<20, func() []limiterStep {
		var out []limiterStep
		for i := int64(0); i < 40; i++ {
			out = append(out, limiterStep{AtMs: i * 100, TextBytes: 5})
		}
		return out
	}()))
	add(run("a steady rate over the limit settles at the limit", 10, 1<<20, func() []limiterStep {
		var out []limiterStep
		for i := int64(0); i < 100; i++ {
			out = append(out, limiterStep{AtMs: i * 20, TextBytes: 5})
		}
		return out
	}()))
	return render("Traces of the sender's rate limiter (the token bucket a guest applies before it writes a progress frame): events and bytes of text per second, a burst of twice the rate, a count of events dropped since the last one that passed. 'steps' are events at 'atMs' milliseconds on an injected clock; 'admitted' says whether the event passes and 'dropped' is the count attached to it when it does. 'pending' is the count still unreported after the last step, which a guest sends as a final marker. Zero or negative limits use the defaults (50 events, 65536 bytes). A binding passes when its limiter reproduces every step. Regenerate with EXT_UPDATE_FIXTURES=1 go test -run TestSDKFixtures ./pkg/plugin-go/contract.",
		[]section{{"cases", cases}})
}

// ---- model-cases.json ----

type descriptorCase struct {
	Name       string `json:"name"`
	Descriptor string `json:"descriptor"`
	Valid      bool   `json:"valid"`
}

type requestCase struct {
	Name    string `json:"name"`
	Request string `json:"request"`
	Valid   bool   `json:"valid"`
	// When the request is valid: whether the call gets a working progress reporter
	// (the operation declared progress and the request carries a rate) and the rate
	// with defaults filled in.
	Reporter bool `json:"reporter,omitempty"`
	Events   int  `json:"events,omitempty"`
	Bytes    int  `json:"bytes,omitempty"`
}

const modelDescriptor = `{"apiVersion":"ext.plugin/v1","identity":{"id":"ctx/conformance","revision":"fixture-1"},"contracts":[{"name":"ext.conformance","version":"v1","operations":[{"name":"echo"},{"name":"long","progress":true},{"name":"wait","progress":false}]}],"reverse":[{"name":"host.svc","version":"v1","operations":[{"name":"sign"},{"name":"slow","surface":"observation"}]}]}`

func buildModelCases() []byte {
	judgeDescriptor := func(raw string) bool {
		var d Descriptor
		return Decode([]byte(raw), &d) == nil && d.Validate() == nil
	}
	var descriptors []any
	add := func(name, raw string) {
		descriptors = append(descriptors, descriptorCase{Name: name, Descriptor: raw, Valid: judgeDescriptor(raw)})
	}
	add("the base descriptor", modelDescriptor)
	for i, raw := range []string{
		`{"apiVersion":"ext.plugin/v1","identity":{"id":"a/b","revision":"r1"},"contracts":[{"name":"c","version":"v1","operations":[{"name":"o","progress":true,"surface":"s"}]}]}`,
		`{"apiVersion":"ext.plugin/v1","identity":{"id":"a/b","revision":"r1"},"contracts":[{"name":"c","version":"v1","operations":[{"name":"o"}]}],"reverse":[]}`,
		`{"apiVersion":"ext.plugin/v1","identity":{"id":"a/b","revision":"r1"},"contracts":[{"name":"c","version":"v1","operations":[{"name":"o"}]}],"reverse":null}`,
		`{"apiVersion":"ext.plugin/v1","identity":{"id":"a/b","revision":"r1"},"contracts":[{"name":"c","version":"v1","operations":[{"name":"o"}]}],"reverse":[{"name":"h","version":"v1","operations":[]}]}`,
		`{"apiVersion":"ext.plugin/v1","identity":{"id":"a/b","revision":"r1"},"contracts":[{"name":"c","version":"v1","operations":[{"name":"o"}]}],"reverse":[{"name":"h","version":"v1","operations":[{"name":"x","progress":true}]}]}`,
		`{"apiVersion":"ext.plugin/v1","identity":{"id":"a/b","revision":"r1"},"contracts":[{"name":"c","version":"v1","operations":[{"name":"o"}]}],"reverse":[{"name":"h","version":"v1","operations":[{"name":"x"}]},{"name":"h","version":"v1","operations":[{"name":"y"}]}]}`,
		`{"apiVersion":"ext.plugin/v1","identity":{"id":"a/b","revision":"r1"},"contracts":[{"name":"c","version":"v1","operations":[{"name":"o"}]}],"reverse":[{"name":"h","version":"v1","operations":[{"name":"x"},{"name":"x"}]}]}`,
		`{"apiVersion":"ext.plugin/v1","identity":{"id":"a/b","revision":"r1"},"contracts":[{"name":"c","version":"v1","operations":[{"name":"o"}]}],"reverse":[{"name":"c","version":"v1","operations":[{"name":"o"}]}]}`,
		`{"apiVersion":"ext.plugin/v1","identity":{"id":"a/b","revision":"r1"},"contracts":[{"name":"c","version":"v1","operations":[{"name":"o","progress":true,"progress":false}]}]}`,
	} {
		add(fmt.Sprintf("hand-made %d", i+1), raw)
	}
	for i, raw := range mutations(parse(modelDescriptor)) {
		add(fmt.Sprintf("mutation %d", i+1), raw)
	}
	var base Descriptor
	if err := Decode([]byte(modelDescriptor), &base); err != nil {
		panic(err)
	}
	var requests []any
	addRequest := func(name, raw string) {
		c := requestCase{Name: name, Request: raw}
		var r Request
		if Decode([]byte(raw), &r) == nil && ValidateRequest(base, r) == nil {
			c.Valid = true
			if op, err := base.Lookup(r.Contract, r.Operation); err == nil && op.Progress && r.Progress != nil {
				limits := r.Progress.Resolved()
				c.Reporter, c.Events, c.Bytes = true, limits.EventsPerSecond, limits.BytesPerSecond
			}
		}
		requests = append(requests, c)
	}
	const requestBase = `{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"ctx/conformance","revision":"fixture-1"},"contract":{"name":"ext.conformance","version":"v1"},"operation":"long","deadline":"2099-01-01T00:00:00Z","payload":{"n":1},"progress":{"eventsPerSecond":7,"bytesPerSecond":900}}`
	addRequest("a request that carries a rate", requestBase)
	for i, raw := range []string{
		`{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"ctx/conformance","revision":"fixture-1"},"contract":{"name":"ext.conformance","version":"v1"},"operation":"long","deadline":"2099-01-01T00:00:00Z","progress":{}}`,
		`{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"ctx/conformance","revision":"fixture-1"},"contract":{"name":"ext.conformance","version":"v1"},"operation":"long","deadline":"2099-01-01T00:00:00Z","progress":{"eventsPerSecond":7}}`,
		`{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"ctx/conformance","revision":"fixture-1"},"contract":{"name":"ext.conformance","version":"v1"},"operation":"long","deadline":"2099-01-01T00:00:00Z","progress":{"bytesPerSecond":900}}`,
		`{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"ctx/conformance","revision":"fixture-1"},"contract":{"name":"ext.conformance","version":"v1"},"operation":"long","deadline":"2099-01-01T00:00:00Z","progress":{"eventsPerSecond":0,"bytesPerSecond":0}}`,
		`{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"ctx/conformance","revision":"fixture-1"},"contract":{"name":"ext.conformance","version":"v1"},"operation":"long","deadline":"2099-01-01T00:00:00Z","progress":{"eventsPerSecond":-5,"bytesPerSecond":-5}}`,
		`{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"ctx/conformance","revision":"fixture-1"},"contract":{"name":"ext.conformance","version":"v1"},"operation":"long","deadline":"2099-01-01T00:00:00Z","progress":{"eventsPerSecond":1048576,"bytesPerSecond":1073741824}}`,
		`{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"ctx/conformance","revision":"fixture-1"},"contract":{"name":"ext.conformance","version":"v1"},"operation":"long","deadline":"2099-01-01T00:00:00Z","progress":null}`,
		`{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"ctx/conformance","revision":"fixture-1"},"contract":{"name":"ext.conformance","version":"v1"},"operation":"long","deadline":"2099-01-01T00:00:00Z"}`,
		`{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"ctx/conformance","revision":"fixture-1"},"contract":{"name":"ext.conformance","version":"v1"},"operation":"echo","deadline":"2099-01-01T00:00:00Z","progress":{"eventsPerSecond":7}}`,
		`{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"ctx/conformance","revision":"fixture-1"},"contract":{"name":"ext.conformance","version":"v1"},"operation":"wait","deadline":"2099-01-01T00:00:00Z","progress":{"eventsPerSecond":7}}`,
		`{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"ctx/conformance","revision":"fixture-1"},"contract":{"name":"ext.conformance","version":"v1"},"operation":"long","deadline":"2099-01-01T00:00:00Z","parent":"1"}`,
		`{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"ctx/conformance","revision":"fixture-1"},"contract":{"name":"ext.conformance","version":"v1"},"operation":"long","deadline":"2099-01-01T00:00:00Z","progress":{"eventsPerSecond":7,"eventsPerSecond":8}}`,
	} {
		addRequest(fmt.Sprintf("hand-made %d", i+1), raw)
	}
	for i, raw := range mutations(parse(requestBase)) {
		addRequest(fmt.Sprintf("mutation %d", i+1), raw)
	}
	return render("The wire model for progress events and reverse calls. 'descriptors' are judged by the Go contract (strict decoding, then the descriptor's own rules); the new members are an operation's 'progress' flag and the descriptor's 'reverse' list of host contracts. 'requests' are judged against 'requestDescriptor' (strict decoding, then the request's own rules); a valid request may carry a 'progress' rate and a 'parent', and 'reporter', 'events' and 'bytes' say whether the call gets a progress reporter and the rate with defaults filled in. A binding passes when it reproduces every outcome. Regenerate with EXT_UPDATE_FIXTURES=1 go test -run TestSDKFixtures ./pkg/plugin-go/contract.",
		[]section{{"descriptors", descriptors}, {"requests", requests}}, `"requestDescriptor": `+modelDescriptor)
}
