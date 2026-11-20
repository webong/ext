package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/webong/ext/res/int/inference"
)

func request() inference.GenerationRequest {
	return inference.GenerationRequest{Model: "gemini-test", MaxOutputTokens: 64, Messages: []inference.Message{{Role: inference.RoleUser, Parts: []inference.Part{{Kind: inference.PartKindText, Text: "hello"}}}}}
}

func TestStream(t *testing.T) {
	chunks := []string{
		`{"responseId":"r1","candidates":[{"content":{"parts":[{"text":"Thinking it over","thought":true}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"text":"Look"}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"text":"ing"}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{"q":"x"}},"thoughtSignature":"sig"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":6,"thoughtsTokenCount":2,"totalTokenCount":12}}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-test:streamGenerateContent" || r.URL.Query().Get("alt") != "sse" {
			t.Errorf("url %s", r.URL)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			_, _ = w.Write([]byte("data: " + c + "\r\n\r\n"))
		}
	}))
	defer server.Close()
	p, err := newProvider(server.URL+"/v1beta", "key")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	var last inference.GenerationEvent
	resp, err := p.Stream(context.Background(), request(), inference.SinkFunc(func(_ context.Context, ev inference.GenerationEvent) error {
		kinds = append(kinds, ev.Kind)
		last = ev
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := "started reasoning.delta text.delta text.delta tool.completed usage completed"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("events %q, want %q", got, want)
	}
	if last.Response == nil || last.Response.StopReason != inference.FinishReasonToolCalls || last.Response.Usage.Reasoning != 2 {
		t.Fatalf("completed %+v", last.Response)
	}
	if len(resp.Message.Parts) != 2 || resp.Message.Parts[0].Kind != inference.PartKindReasoning || resp.Message.Parts[1].Text != "Looking" {
		t.Fatalf("parts %+v", resp.Message.Parts)
	}
	if len(resp.Message.ToolCalls) != 1 || !strings.Contains(string(resp.Message.ToolCalls[0].ProviderData), "sig") {
		t.Fatalf("tool calls %+v", resp.Message.ToolCalls)
	}
}

func TestStreamWithoutFinishFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"partial\"}]}}]}\n\n"))
	}))
	defer server.Close()
	p, _ := newProvider(server.URL+"/v1beta", "key")
	_, err := p.Stream(context.Background(), request(), inference.SinkFunc(func(context.Context, inference.GenerationEvent) error { return nil }))
	if !errors.Is(err, inference.ErrInvalidResponse) {
		t.Fatalf("truncated stream: %v", err)
	}
}

func TestSignatureAndOptionsRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		config := body["generationConfig"].(map[string]any)
		if config["topK"] != float64(3) || body["safetySettings"] == nil {
			t.Errorf("options not placed: %v", body)
		}
		model := body["contents"].([]any)[1].(map[string]any)["parts"].([]any)[0].(map[string]any)
		if model["thoughtSignature"] != "sig" || model["functionCall"] == nil {
			t.Errorf("call sent back as %v", model)
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`))
	}))
	defer server.Close()
	p, _ := newProvider(server.URL+"/v1beta", "key")
	req := request()
	req.Options = json.RawMessage(`{"topK":3,"safetySettings":[]}`)
	req.Messages = append(req.Messages,
		inference.Message{Role: inference.RoleAssistant, ToolCalls: []inference.ToolCall{{ID: "gemini-call-0", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`), ProviderData: json.RawMessage(`{"thoughtSignature":"sig"}`)}}},
		inference.Message{Role: inference.RoleTool, ToolCallID: "gemini-call-0", ToolName: "lookup", Parts: []inference.Part{{Kind: inference.PartKindText, Text: "found"}}},
	)
	if _, err := p.Generate(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	req.Options = json.RawMessage(`{"contents":[]}`)
	if _, err := p.Generate(context.Background(), req); !errors.Is(err, inference.ErrUnsupported) {
		t.Fatalf("unknown option accepted: %v", err)
	}
}

func TestEmbed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/text-embedding-004:batchEmbedContents" {
			t.Errorf("path %s", r.URL.Path)
		}
		var body struct {
			Requests []map[string]any `json:"requests"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Requests) != 2 || body.Requests[0]["taskType"] != "RETRIEVAL_QUERY" || body.Requests[0]["model"] != "models/text-embedding-004" {
			t.Errorf("requests %v", body.Requests)
		}
		_, _ = w.Write([]byte(`{"embeddings":[{"values":[0.1,0.2]},{"values":[0.3,0.4]}]}`))
	}))
	defer server.Close()
	p, _ := newProvider(server.URL+"/v1beta", "key")
	resp, err := p.Embed(context.Background(), inference.EmbeddingRequest{Model: "text-embedding-004", Inputs: []string{"a", "b"}, Options: json.RawMessage(`{"taskType":"RETRIEVAL_QUERY"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Embeddings) != 2 || resp.Embeddings[1].Index != 1 || resp.Embeddings[1].Vector[1] != 0.4 {
		t.Fatalf("embeddings %+v", resp.Embeddings)
	}
}

func TestOtherFinishReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"x"}]},"finishReason":"OTHER"}]}`))
	}))
	defer server.Close()
	p, _ := newProvider(server.URL+"/v1beta", "key")
	resp, err := p.Generate(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != inference.FinishReasonOther || resp.ProviderStopReason != "OTHER" {
		t.Fatalf("got %s/%q", resp.StopReason, resp.ProviderStopReason)
	}
}
