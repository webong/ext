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

func TestToolsImagesAndStructuredOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		first := body["contents"].([]any)[0].(map[string]any)["parts"].([]any)
		if first[1].(map[string]any)["inlineData"] == nil || body["tools"] == nil || body["generationConfig"].(map[string]any)["responseJsonSchema"] == nil {
			t.Errorf("missing image, tools, or schema: %v", body)
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"id":"call-1","name":"lookup","args":{"q":"x"}}}]},"finishReason":"STOP"}]}`))
	}))
	defer server.Close()
	p, err := newProvider(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	request := inference.GenerationRequest{Model: "test-model", Messages: []inference.Message{{Role: inference.RoleUser, Parts: []inference.Part{{Kind: inference.PartKindText, Text: "hello"}, {Kind: inference.PartKindImage, MediaType: "image/png", Data: []byte{1, 2}}}}}, Tools: []inference.ToolDefinition{{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}}, OutputSchema: json.RawMessage(`{"type":"object"}`)}
	result, err := p.Generate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != inference.FinishReasonToolCalls || len(result.Message.ToolCalls) != 1 || result.Message.ToolCalls[0].ID != "call-1" {
		t.Fatalf("tool response: %+v", result)
	}
}

func TestGenerateRejectsUnsupportedResponsePart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"unknown":{"value":1}}]},"finishReason":"STOP"}]}`))
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

func TestToolResultRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Contents []struct {
				Role  string           `json:"role"`
				Parts []map[string]any `json:"parts"`
			} `json:"contents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if len(body.Contents) != 3 || body.Contents[1].Parts[0]["functionCall"] == nil || body.Contents[2].Parts[0]["functionResponse"] == nil {
			t.Errorf("tool exchange: %+v", body.Contents)
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}]}`))
	}))
	defer server.Close()
	p, err := newProvider(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	r := inference.GenerationRequest{Model: "test-model", Messages: []inference.Message{
		{Role: inference.RoleUser, Parts: []inference.Part{{Kind: inference.PartKindText, Text: "look"}}},
		{Role: inference.RoleAssistant, ToolCalls: []inference.ToolCall{{ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)}}},
		{Role: inference.RoleTool, ToolCallID: "call-1", ToolName: "lookup", Parts: []inference.Part{{Kind: inference.PartKindText, Text: "found"}}},
	}}
	result, err := p.Generate(context.Background(), r)
	if err != nil || result.Text() != "done" {
		t.Fatalf("tool result: %+v, %v", result, err)
	}
}

func TestFunctionCallWithoutProviderID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{}}}]},"finishReason":"STOP"}]}`))
	}))
	defer server.Close()
	p, err := newProvider(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	r := inference.GenerationRequest{Model: "test-model", Messages: []inference.Message{{Role: inference.RoleUser, Parts: []inference.Part{{Kind: inference.PartKindText, Text: "look"}}}}}
	result, err := p.Generate(context.Background(), r)
	if err != nil || len(result.Message.ToolCalls) != 1 || result.Message.ToolCalls[0].ID == "" {
		t.Fatalf("optional function ID: %+v, %v", result, err)
	}
}
