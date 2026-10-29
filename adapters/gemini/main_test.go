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
		if r.URL.Path != "/v1beta/models/test-model:generateContent" || r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("bad request: %s %v", r.URL.Path, r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body["contents"].([]any)) != 1 {
			t.Errorf("bad body: %v", body)
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"works"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`))
	}))
	defer server.Close()
	p, err := newProvider(server.URL+"/v1beta", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	req := inference.GenerationRequest{Model: "test-model", Messages: []inference.Message{{Role: inference.RoleUser, Parts: []inference.Part{{Kind: inference.PartKindText, Text: "hello"}}}}}
	result, err := p.Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text() != "works" || result.Usage.Total != 3 {
		t.Fatalf("bad result: %+v", result)
	}
}

func TestGenerateRejectsUnsupportedResponsePart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"tool"}}]},"finishReason":"STOP"}]}`))
	}))
	defer server.Close()
	p, err := newProvider(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	request := inference.GenerationRequest{Model: "test-model", Messages: []inference.Message{{Role: inference.RoleUser, Parts: []inference.Part{{Kind: inference.PartKindText, Text: "hello"}}}}}
	if _, err := p.Generate(context.Background(), request); !errors.Is(err, inference.ErrUnsupported) {
		t.Fatalf("unsupported part: %v", err)
	}
}
