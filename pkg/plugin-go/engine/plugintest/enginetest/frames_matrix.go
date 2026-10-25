package enginetest

import (
	"encoding/json"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/engine"
)

// The reverse and progress declarations must be judged the same by the Go
// contract and the C engine: a descriptor that uses them, a request that
// carries the host's progress rate, and whether two descriptors match.

func (e Engine) framesFixture(t *testing.T) (json.RawMessage, map[string]any, plugin.Descriptor) {
	t.Helper()
	data, err := e.fixture(t, "v1/descriptor-frames.json")
	if err != nil {
		t.Fatal(err)
	}
	var base map[string]any
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatal(err)
	}
	var d plugin.Descriptor
	if err := plugin.Decode(data, &d); err != nil || d.Validate() != nil {
		t.Fatalf("fixture invalid: %v", err)
	}
	if len(d.Reverse) == 0 || !d.Contracts[0].Operations[1].Progress {
		t.Fatal("the fixture must use reverse and progress")
	}
	return data, base, d
}

func goValidates(raw json.RawMessage) error {
	var v plugin.Descriptor
	if err := plugin.Decode(raw, &v); err != nil {
		return err
	}
	return v.Validate()
}

func FramesDescriptorDifferential(t *testing.T, e Engine) {
	_, base, _ := e.framesFixture(t)
	var accepted, total, diverged int
	for _, raw := range mutations(base) {
		total++
		want := goValidates(raw)
		_, got := e.Call("descriptor.validate", raw)
		if want == nil {
			accepted++
		}
		if (want == nil) != (got == nil) {
			diverged++
			if diverged <= 8 {
				t.Errorf("%s Go=%s C=%s", raw, errText(want), errText(got))
			}
		}
	}
	t.Logf("frames descriptor: %d mutations (%d accepted), %d divergences", total, accepted, diverged)
	if accepted == 0 || accepted == total {
		t.Fatalf("mutations are vacuous: accepted %d of %d", accepted, total)
	}
}

func FramesRequestDifferential(t *testing.T, e Engine) {
	data, base, d := e.framesFixture(t)
	request := map[string]any{"apiVersion": "ext.plugin/v1", "id": "1", "plugin": base["identity"], "contract": map[string]any{"name": "ext.conformance", "version": "v1"}, "operation": "long", "payload": map[string]any{"value": 7}, "deadline": "2099-01-01T00:00:00Z", "progress": map[string]any{"eventsPerSecond": 10, "bytesPerSecond": 4096}, "parent": "p1"}
	var accepted, total, diverged int
	for _, raw := range mutations(request) {
		total++
		var r plugin.Request
		want := plugin.Decode(raw, &r)
		if want == nil {
			want = plugin.ValidateRequest(d, r)
		}
		_, got := e.service(t, "request.validate", map[string]any{"descriptor": json.RawMessage(data), "request": raw})
		if want == nil {
			accepted++
		}
		if (want == nil) != (got == nil) {
			diverged++
			if diverged <= 8 {
				t.Errorf("%s Go=%s C=%s", raw, errText(want), errText(got))
			}
		}
	}
	t.Logf("frames request: %d mutations (%d accepted), %d divergences", total, accepted, diverged)
	if accepted == 0 || accepted == total {
		t.Fatalf("mutations are vacuous: accepted %d of %d", accepted, total)
	}
}

func FramesMatchDifferential(t *testing.T, e Engine) {
	data, base, selected := e.framesFixture(t)
	var matched, total, diverged int
	for _, raw := range mutations(base) {
		var actual plugin.Descriptor
		if plugin.Decode(raw, &actual) != nil || actual.Validate() != nil {
			continue
		}
		total++
		want := plugin.MatchHandshake(selected, actual)
		_, got := e.service(t, "descriptor.match", map[string]any{"selected": json.RawMessage(data), "actual": raw})
		if want == nil {
			matched++
		}
		if (want == nil) != (got == nil) {
			diverged++
			if diverged <= 8 {
				t.Errorf("%s Go=%s C=%s", raw, errText(want), errText(got))
			}
		}
	}
	t.Logf("frames match: %d valid variants (%d match), %d divergences", total, matched, diverged)
	if total == 0 {
		t.Fatal("no valid variants to compare")
	}
}
