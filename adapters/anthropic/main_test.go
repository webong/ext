package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/webong/ext/res/inference"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "test-key" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("bad request: %s %v", r.URL.Path, r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "test-model" || body["max_tokens"] != float64(16) {
			t.Errorf("bad body: %v", body)
		}
		_, _ = w.Write([]byte(`{"id":"m1","model":"test-model","content":[{"type":"text","text":"works"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`))
	}))
	defer server.Close()
	p, err := newProvider(server.URL+"/v1", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	req := inference.GenerationRequest{Model: "test-model", MaxOutputTokens: 16, Messages: []inference.Message{{Role: inference.RoleUser, Parts: []inference.Part{{Kind: inference.PartKindText, Text: "hello"}}}}}
	result, err := p.Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text() != "works" || result.Usage.Total != 3 {
		t.Fatalf("bad result: %+v", result)
	}
}

func TestGenerateRequiresMaxOutputTokens(t *testing.T) {
	p, err := newProvider("https://example.invalid/v1", "key")
	if err != nil {
		t.Fatal(err)
	}
	request := inference.GenerationRequest{Model: "test-model", Messages: []inference.Message{{Role: inference.RoleUser, Parts: []inference.Part{{Kind: inference.PartKindText, Text: "hello"}}}}}
	if _, err := p.Generate(context.Background(), request); !errors.Is(err, inference.ErrInvalidRequest) {
		t.Fatalf("missing token limit: %v", err)
	}
}
