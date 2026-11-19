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
	return inference.GenerationRequest{Model: "test-model", MaxOutputTokens: 64, Messages: []inference.Message{{Role: inference.RoleUser, Parts: []inference.Part{{Kind: inference.PartKindText, Text: "hello"}}}}}
}

func TestStream(t *testing.T) {
	events := []string{
		`{"type":"message_start","message":{"id":"msg_1","model":"test-model","usage":{"input_tokens":10,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me look."}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Checking"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"lookup","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"x\"}"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}`,
		`{"type":"message_stop"}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != true {
			t.Errorf("stream = %v", body["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			_, _ = w.Write([]byte("event: x\ndata: " + e + "\n\n"))
		}
	}))
	defer server.Close()
	p, err := newProvider(server.URL+"/v1", "key")
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
	want := "started reasoning.delta text.delta tool.delta tool.delta tool.completed usage completed"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("events %q, want %q", got, want)
	}
	if last.Response == nil || last.Response.StopReason != inference.FinishReasonToolCalls {
		t.Fatalf("completed %+v", last.Response)
	}
	if resp.Usage.Input != 10 || resp.Usage.Output != 20 {
		t.Fatalf("usage %+v", resp.Usage)
	}
	if len(resp.Message.Parts) != 2 || resp.Message.Parts[0].Kind != inference.PartKindReasoning || resp.Message.Parts[0].Text != "Let me look." || !strings.Contains(string(resp.Message.Parts[0].ProviderData), "sig") || resp.Message.Parts[1].Text != "Checking" {
		t.Fatalf("parts %+v", resp.Message.Parts)
	}
	if len(resp.Message.ToolCalls) != 1 || string(resp.Message.ToolCalls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("tool calls %+v", resp.Message.ToolCalls)
	}
}

func TestStreamWithoutStopFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"model\":\"test-model\"}}\n\n"))
	}))
	defer server.Close()
	p, _ := newProvider(server.URL+"/v1", "key")
	_, err := p.Stream(context.Background(), request(), inference.SinkFunc(func(context.Context, inference.GenerationEvent) error { return nil }))
	if !errors.Is(err, inference.ErrInvalidResponse) {
		t.Fatalf("truncated stream: %v", err)
	}
}

func TestOptionsAndThinkingRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["top_k"] != float64(5) {
			t.Errorf("top_k = %v", body["top_k"])
		}
		thinking, _ := body["thinking"].(map[string]any)
		if thinking["type"] != "enabled" {
			t.Errorf("thinking = %v", body["thinking"])
		}
		assistant := body["messages"].([]any)[1].(map[string]any)["content"].([]any)[0].(map[string]any)
		if assistant["type"] != "thinking" || assistant["signature"] != "sig" {
			t.Errorf("reasoning sent back as %v", assistant)
		}
		_, _ = w.Write([]byte(`{"id":"m","model":"test-model","content":[{"type":"redacted_thinking","data":"opaque"},{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()
	p, _ := newProvider(server.URL+"/v1", "key")
	req := request()
	req.Options = json.RawMessage(`{"top_k":5,"thinking":{"type":"enabled","budget_tokens":1024}}`)
	req.Messages = append(req.Messages,
		inference.Message{Role: inference.RoleAssistant, Parts: []inference.Part{{Kind: inference.PartKindReasoning, Text: "earlier", ProviderData: json.RawMessage(`{"signature":"sig"}`)}}},
		inference.Message{Role: inference.RoleUser, Parts: []inference.Part{{Kind: inference.PartKindText, Text: "go on"}}},
	)
	resp, err := p.Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Parts[0].Kind != inference.PartKindContext || !strings.Contains(string(resp.Message.Parts[0].ProviderData), "opaque") {
		t.Fatalf("redacted thinking %+v", resp.Message.Parts[0])
	}

	req.Options = json.RawMessage(`{"model":"other"}`)
	if _, err := p.Generate(context.Background(), req); !errors.Is(err, inference.ErrUnsupported) {
		t.Fatalf("unknown option accepted: %v", err)
	}
}

func TestListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-x","display_name":"Claude X"}]}`))
	}))
	defer server.Close()
	p, _ := newProvider(server.URL+"/v1", "key")
	models, err := p.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "claude-x" {
		t.Fatalf("models %+v, %v", models, err)
	}
}

func TestStopReasons(t *testing.T) {
	for raw, want := range map[string]inference.FinishReason{"pause_turn": inference.FinishReasonPaused, "something_new": inference.FinishReasonOther, "end_turn": inference.FinishReasonStop} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"id":"m","model":"test-model","content":[{"type":"text","text":"x"}],"stop_reason":"` + raw + `","usage":{"input_tokens":1,"output_tokens":1}}`))
		}))
		p, _ := newProvider(server.URL+"/v1", "key")
		resp, err := p.Generate(context.Background(), request())
		server.Close()
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		wantRaw := raw
		if want == inference.FinishReasonStop {
			wantRaw = ""
		}
		if resp.StopReason != want || resp.ProviderStopReason != wantRaw {
			t.Fatalf("%s: got %s/%q", raw, resp.StopReason, resp.ProviderStopReason)
		}
	}
}
