package client

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/webong/ext/res/inference"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerateUsesConfiguredCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-secret" {
			t.Errorf("authorization = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "test-model" {
			t.Errorf("model = %v", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"works"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()
	p, err := NewProvider(server.URL+"/v1", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	req := inference.GenerationRequest{Model: "test-model", Messages: []inference.Message{{Role: inference.RoleUser, Parts: []inference.Part{{Kind: inference.PartKindText, Text: "hello"}}}}}
	resp, err := p.Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.Parts) != 1 || resp.Message.Parts[0].Text != "works" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestUnsupportedMediaIsRejected(t *testing.T) {
	p, err := NewProvider("https://example.test/v1", "secret")
	if err != nil {
		t.Fatal(err)
	}
	req := inference.GenerationRequest{Model: "test-model", Messages: []inference.Message{{Role: inference.RoleUser, Parts: []inference.Part{{Kind: inference.PartKindImage, MediaType: "image/png", Data: []byte{1}}}}}}
	_, err = p.Generate(context.Background(), req)
	if !errors.Is(err, inference.ErrUnsupported) {
		t.Fatalf("media silently dropped: %v", err)
	}
}

func TestIncompleteStreamFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"))
	}))
	defer server.Close()
	p, err := NewProvider(server.URL+"/v1", "secret")
	if err != nil {
		t.Fatal(err)
	}
	req := inference.GenerationRequest{Model: "test-model", Messages: []inference.Message{{Role: inference.RoleUser, Parts: []inference.Part{{Kind: inference.PartKindText, Text: "hello"}}}}}
	_, err = p.Stream(context.Background(), req, inference.SinkFunc(func(context.Context, inference.GenerationEvent) error { return nil }))
	if !errors.Is(err, inference.ErrInvalidResponse) {
		t.Fatalf("truncated stream accepted: %v", err)
	}
}
