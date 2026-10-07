//go:build ctx_cengine && cgo && (darwin || linux)

package goengine

import (
	"encoding/json"
	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/schema"
	"testing"
)

func TestWireOptionalNullsAndErrors(t *testing.T) {
	descriptor := json.RawMessage(`{"apiVersion":"ext.plugin/v1","identity":{"id":"test","revision":"r1","version":null},"contracts":[{"name":"test","version":"v1","operations":[{"name":"echo","surface":null}]}]}`)
	var d plugin.Descriptor
	if err := plugin.Decode(descriptor, &d); err != nil {
		t.Fatal(err)
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := EngineCall("descriptor.validate", descriptor); err != nil {
		t.Fatal(err)
	}
	request := json.RawMessage(`{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"test","revision":"r1"},"contract":{"name":"test","version":"v1"},"operation":"echo","surface":null,"deadline":"2099-01-01T00:00:00Z"}`)
	if _, err := service(t, "request.validate", map[string]any{"descriptor": descriptor, "request": request}); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"code":"busy"}`, `{"code":"busy","message":null}`, `{"code":"busy","retryAfterMilliseconds":null}`, `{"code":"busy","retryAfterMilliseconds":-0}`, `{"code":"busy","retryAfterMilliseconds":1e2}`, `{"code":"busy","retryAfterMilliseconds":-1}`, `{"code":"busy","message":7}`} {
		raw := json.RawMessage(`{"apiVersion":"ext.plugin/v1","id":"1","error":` + body + `}`)
		var r plugin.Response
		want := plugin.Decode(raw, &r)
		if want == nil {
			want = r.Validate("1")
		}
		_, got := service(t, "response.validate", map[string]any{"requestID": "1", "response": raw})
		if (want == nil) != (got == nil) {
			t.Fatalf("%s: Go=%v, C=%v", body, want, got)
		}
	}
}
func TestSchemaNumbersDifferential(t *testing.T) {
	values := []string{"null", "true", `"text"`, "0", "-0", "0.1", "1.0", "1e2", "1e-999", "1e999", "9007199254740993", "-9223372036854775809", "1.7976931348623157e308", "4.9406564584124654e-324"}
	for _, kind := range []string{"number", "integer", "null", "boolean"} {
		s := schema.Schema{Type: kind}
		if CheckSchema(s, nil) == nil {
			t.Fatal("empty payload accepted")
		}
		for _, v := range values {
			want := s.Check(json.RawMessage(v))
			got := CheckSchema(s, json.RawMessage(v))
			if (want == nil) != (got == nil) {
				t.Fatalf("%s %s: Go=%v, C=%v", kind, v, want, got)
			}
		}
	}
}
