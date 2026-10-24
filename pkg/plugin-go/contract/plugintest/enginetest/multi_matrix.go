package enginetest

import (
	"encoding/json"
	"math/rand"
	"os"
	"strconv"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	packagekit "github.com/webong/ext/pkg/plugin-go/contract/package"
)

// multiMutations returns count documents, each differing from doc at k independent
// positions (none inside another), each set to an edge value or removed. Sampling is
// seeded, so a failure reproduces. pairMutations is the k = 2 case.
func multiMutations(doc any, k, count int, seed int64) []json.RawMessage {
	rng := rand.New(rand.NewSource(seed))
	all := positions(doc)
	edges := append(edgeValues(), removeMarker)
	var out []json.RawMessage
	misses := 0
	for len(out) < count {
		if misses > 1000 {
			return out // the document has no k independent positions
		}
		var chosen []position
		for attempts := 0; len(chosen) < k && attempts < 100; attempts++ {
			candidate := all[rng.Intn(len(all))]
			clear := true
			for _, c := range chosen {
				if overlaps(c, candidate) {
					clear = false
					break
				}
			}
			if clear {
				chosen = append(chosen, candidate)
			}
		}
		if len(chosen) < k {
			misses++
			continue
		}
		// Apply the later positions first, so removing an array element cannot shift another.
		for i := 1; i < len(chosen); i++ {
			for j := i; j > 0 && later(chosen[j], chosen[j-1]); j-- {
				chosen[j], chosen[j-1] = chosen[j-1], chosen[j]
			}
		}
		clone := deepCopy(doc)
		for _, p := range chosen {
			clone = setAt(clone, p, edges[rng.Intn(len(edges))])
		}
		raw, err := json.Marshal(clone)
		if err != nil {
			continue
		}
		out = append(out, raw)
	}
	return out
}

func (e Engine) readJSON(t *testing.T, name string) (json.RawMessage, map[string]any) {
	t.Helper()
	data, err := e.fixture(t, name)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return data, doc
}

// MultiFieldMutationDifferential changes three and four independent positions of a
// valid descriptor, request, response, progress frame, reverse request and package
// manifest at once and requires the C engine and the Go code to agree on whether the
// result is valid. The descriptor and request documents also carry the members that
// progress events and reverse calls added: an operation's progress flag, the reverse
// list, a request's rate and parent. EXT_PAIR_SCALE multiplies every sample size.
func MultiFieldMutationDifferential(t *testing.T, e Engine) {
	data, base := e.readJSON(t, "v1/descriptor.json")
	framesData, framesBase := e.readJSON(t, "v1/frames-descriptor.json")
	var d, fd plugin.Descriptor
	if err := plugin.Decode(data, &d); err != nil || d.Validate() != nil {
		t.Fatalf("fixture invalid: %v", err)
	}
	if err := plugin.Decode(framesData, &fd); err != nil || fd.Validate() != nil {
		t.Fatalf("frames fixture invalid: %v", err)
	}
	request := map[string]any{"apiVersion": "ext.plugin/v1", "id": "1", "plugin": base["identity"], "contract": map[string]any{"name": "ext.conformance", "version": "v1"}, "operation": "echo", "payload": map[string]any{"value": 7}, "deadline": "2099-01-01T00:00:00Z"}
	framesRequest := map[string]any{"apiVersion": "ext.plugin/v1", "id": "1", "plugin": framesBase["identity"], "contract": map[string]any{"name": "ext.frames", "version": "v1"}, "operation": "stream", "payload": map[string]any{"value": 7}, "deadline": "2099-01-01T00:00:00Z", "progress": map[string]any{"eventsPerSecond": 7, "bytesPerSecond": 900}, "parent": "p1"}
	response := map[string]any{"apiVersion": "ext.plugin/v1", "id": "1", "payload": map[string]any{"value": 7}}
	failure := map[string]any{"apiVersion": "ext.plugin/v1", "id": "1", "error": map[string]any{"code": "busy", "message": "m", "retryAfterMilliseconds": 10}}
	progress := map[string]any{"frame": "progress", "apiVersion": "ext.plugin/v1", "parent": "1", "kind": "log", "stream": "out", "text": "hello", "completed": 1, "total": 3, "dropped": 2}
	reverseRequest := map[string]any{"frame": "reverse_request", "apiVersion": "ext.plugin/v1", "id": "g1", "parent": "1",
		"contract": map[string]any{"name": "host.svc", "version": "v1"}, "operation": "sign", "deadline": "2099-01-01T00:00:00Z", "payload": map[string]any{"n": 1}}
	manifest, _ := manifestDocument(t)

	scale := 1
	if v, err := strconv.Atoi(os.Getenv("EXT_PAIR_SCALE")); err == nil && v > 0 {
		scale = v
	}
	accepted := map[string]int{}
	run := func(name string, doc any, k, count int, seed int64, check func(raw json.RawMessage) (want, got error)) {
		var total, valid, diverged int
		for _, raw := range multiMutations(doc, k, count*scale, seed) {
			want, got := check(raw)
			total++
			if want == nil {
				valid++
			}
			if (want == nil) != (got == nil) {
				diverged++
				if diverged <= 8 {
					t.Errorf("%s (%d fields): %s Go=%s C=%s", name, k, raw, errText(want), errText(got))
				}
			}
		}
		accepted[name] += valid
		t.Logf("%s, %d fields: %d mutations (%d accepted), %d divergences", name, k, total, valid, diverged)
		if total > 0 && valid == total {
			t.Errorf("%s mutations of %d fields are vacuous: all %d were accepted", name, k, total)
		}
	}
	descriptorCheck := func(raw json.RawMessage) (error, error) {
		var v plugin.Descriptor
		want := plugin.Decode(raw, &v)
		if want == nil {
			want = v.Validate()
		}
		_, got := e.Call("descriptor.validate", raw)
		return want, got
	}
	requestCheck := func(selected plugin.Descriptor, selectedJSON json.RawMessage) func(json.RawMessage) (error, error) {
		return func(raw json.RawMessage) (error, error) {
			var r plugin.Request
			want := plugin.Decode(raw, &r)
			if want == nil {
				want = plugin.ValidateRequest(selected, r)
			}
			_, got := e.service(t, "request.validate", map[string]any{"descriptor": selectedJSON, "request": raw})
			return want, got
		}
	}
	responseCheck := func(raw json.RawMessage) (error, error) {
		var r plugin.Response
		want := plugin.Decode(raw, &r)
		if want == nil {
			want = r.Validate("1")
		}
		_, got := e.service(t, "response.validate", map[string]any{"requestID": "1", "response": raw})
		return want, got
	}
	frameCheck := func(raw json.RawMessage) (error, error) {
		_, got := e.Call("frame.validate", raw)
		return goFrameValidates(raw), got
	}
	manifestCheck := func(raw json.RawMessage) (error, error) {
		_, want := packagekit.Decode(raw)
		_, got := e.Call("package.validate", raw)
		return want, got
	}
	for k := 3; k <= 4; k++ {
		seed := int64(100 * k)
		run("descriptor", base, k, 1500, seed+1, descriptorCheck)
		run("descriptor with progress and reverse", framesBase, k, 1500, seed+2, descriptorCheck)
		run("request", request, k, 1500, seed+3, requestCheck(d, data))
		run("request with progress and parent", framesRequest, k, 1500, seed+4, requestCheck(fd, framesData))
		run("response", response, k, 1000, seed+5, responseCheck)
		run("failure", failure, k, 1000, seed+6, responseCheck)
		run("progress frame", progress, k, 1000, seed+7, frameCheck)
		run("reverse request frame", reverseRequest, k, 1000, seed+8, frameCheck)
		run("manifest", manifest, k, 2000, seed+9, manifestCheck)
	}
	// A document whose mutations are never valid would let a C engine that refuses too much pass.
	for name, n := range accepted {
		if n == 0 && name != "response" && name != "request" {
			t.Errorf("%s: no mutation of three or four fields was valid", name)
		}
	}
}
