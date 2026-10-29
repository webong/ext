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
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "test-key" || r.Header.Get("anthropic-version") != "2023-06-01" {
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

func TestToolsImagesAndStructuredOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		messages := body["messages"].([]any)
		first := messages[0].(map[string]any)["content"].([]any)
		if first[1].(map[string]any)["type"] != "image" {
			t.Errorf("image mapping: %v", first)
		}
		if body["tools"] == nil || body["output_config"] == nil {
			t.Errorf("missing tools or schema: %v", body)
		}
		_, _ = w.Write([]byte(`{"id":"m1","model":"test-model","content":[{"type":"tool_use","id":"call-1","name":"lookup","input":{"q":"x"}}],"stop_reason":"tool_use","usage":{"input_tokens":2,"output_tokens":1}}`))
	}))
	defer server.Close()
	p, err := newProvider(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	r := inference.GenerationRequest{Model: "test-model", MaxOutputTokens: 16,
		Messages: []inference.Message{{Role: inference.RoleUser, Parts: []inference.Part{{Kind: inference.PartKindText, Text: "look"}, {Kind: inference.PartKindImage, MediaType: "image/png", Data: []byte{1, 2}}}}},
		Tools:    []inference.ToolDefinition{{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}}, OutputSchema: json.RawMessage(`{"type":"object"}`)}
	result, err := p.Generate(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != inference.FinishReasonToolCalls || len(result.Message.ToolCalls) != 1 || result.Message.ToolCalls[0].ID != "call-1" {
		t.Fatalf("tool response: %+v", result)
	}
}

func TestToolResultRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string           `json:"role"`
				Content []map[string]any `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if len(body.Messages) != 3 || body.Messages[1].Content[0]["type"] != "tool_use" ||
			body.Messages[2].Content[0]["type"] != "tool_result" || body.Messages[2].Content[0]["tool_use_id"] != "call-1" {
			t.Errorf("tool exchange: %+v", body.Messages)
		}
		_, _ = w.Write([]byte(`{"id":"m2","model":"test-model","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}`))
	}))
	defer server.Close()
	p, err := newProvider(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	r := inference.GenerationRequest{Model: "test-model", MaxOutputTokens: 16, Messages: []inference.Message{
		{Role: inference.RoleUser, Parts: []inference.Part{{Kind: inference.PartKindText, Text: "look"}}},
		{Role: inference.RoleAssistant, ToolCalls: []inference.ToolCall{{ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)}}},
		{Role: inference.RoleTool, ToolCallID: "call-1", ToolName: "lookup", Parts: []inference.Part{{Kind: inference.PartKindText, Text: "found"}}},
	}}
	result, err := p.Generate(context.Background(), r)
	if err != nil || result.Text() != "done" {
		t.Fatalf("tool result: %+v, %v", result, err)
	}
}
