package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/webong/ext/res/int/inference"
)

func userRequest() inference.GenerationRequest {
	return inference.GenerationRequest{Model: "test-model", Messages: []inference.Message{{Role: inference.RoleUser, Parts: []inference.Part{{Kind: inference.PartKindText, Text: "hello"}}}}}
}

func TestStreamEmitsToolCallsUsageAndCompleted(t *testing.T) {
	chunks := []string{
		`{"id":"c1","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"Look"}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{"content":"ing"}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"id":"c1","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if opts, _ := body["stream_options"].(map[string]any); opts["include_usage"] != true {
			t.Errorf("stream_options = %v", body["stream_options"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	p, err := NewProvider(server.URL+"/v1", "secret")
	if err != nil {
		t.Fatal(err)
	}
	var events []inference.GenerationEvent
	resp, err := p.Stream(context.Background(), userRequest(), inference.SinkFunc(func(_ context.Context, ev inference.GenerationEvent) error {
		events = append(events, ev)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, ev := range events {
		kinds = append(kinds, ev.Kind)
	}
	want := "started text.delta text.delta tool.delta tool.delta usage tool.completed completed"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("events %q, want %q", got, want)
	}
	last := events[len(events)-1].Response
	if last == nil || last.StopReason != inference.FinishReasonToolCalls || last.Usage.Total != 8 {
		t.Fatalf("completed response %+v", last)
	}
	if len(resp.Message.Parts) != 1 || resp.Message.Parts[0].Text != "Looking" {
		t.Fatalf("text %+v", resp.Message.Parts)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].ID != "call_1" || string(resp.Message.ToolCalls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("tool calls %+v", resp.Message.ToolCalls)
	}
}

func TestToolCallsRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls, _ := body.Messages[1]["tool_calls"].([]any)
		fn, _ := calls[0].(map[string]any)["function"].(map[string]any)
		if fn["arguments"] != `{"q":"x"}` {
			t.Errorf("history arguments = %v", fn["arguments"])
		}
		if body.Messages[2]["tool_call_id"] != "call_1" || body.Messages[2]["role"] != "tool" {
			t.Errorf("tool result = %v", body.Messages[2])
		}
		_, _ = w.Write([]byte(`{"id":"1","model":"m","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_2","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"y\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()
	p, err := NewProvider(server.URL+"/v1", "secret")
	if err != nil {
		t.Fatal(err)
	}
	req := userRequest()
	req.Messages = append(req.Messages,
		inference.Message{Role: inference.RoleAssistant, ToolCalls: []inference.ToolCall{{ID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)}}},
		inference.Message{Role: inference.RoleTool, ToolCallID: "call_1", Parts: []inference.Part{{Kind: inference.PartKindText, Text: "found"}}},
	)
	resp, err := p.Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 || string(resp.Message.ToolCalls[0].Arguments) != `{"q":"y"}` {
		t.Fatalf("tool calls %+v", resp.Message.ToolCalls)
	}
}

func TestSynthesizeStreamsAudio(t *testing.T) {
	audio := []byte{0xff, 0xf3, 0x00, 0x80, 'I', 'D', '3'}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["voice"] != "alloy" || body["response_format"] != "mp3" {
			t.Errorf("body = %v", body)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(audio)
	}))
	defer server.Close()
	p, err := NewProvider(server.URL+"/v1", "secret")
	if err != nil {
		t.Fatal(err)
	}
	s, err := p.Synthesize(context.Background(), inference.SpeechRequest{Model: "tts-1", Input: "hi", Voice: "alloy", Format: "mp3"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(s)
	_ = s.Close()
	if err != nil || !bytes.Equal(got, audio) {
		t.Fatalf("audio %x, %v", got, err)
	}
}

func TestTranscribeSendsMultipart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("model") != "whisper-1" || r.FormValue("response_format") != "verbose_json" || r.Form["timestamp_granularities[]"][0] != "word" {
			t.Errorf("form = %v", r.Form)
		}
		f, h, err := r.FormFile("file")
		if err != nil || h.Filename != "audio.wav" {
			t.Fatalf("file %v %v", h, err)
		}
		data, _ := io.ReadAll(f)
		if string(data) != "RIFF" {
			t.Errorf("audio = %q", data)
		}
		_, _ = w.Write([]byte(`{"text":"hello there","language":"english","duration":1.5,"words":[{"word":"hello","start":0,"end":0.5}]}`))
	}))
	defer server.Close()
	p, err := NewProvider(server.URL+"/v1", "secret")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Transcribe(context.Background(), inference.TranscriptionRequest{Model: "whisper-1", Audio: []byte("RIFF"), MediaType: "audio/wav", Timestamps: []string{"word"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hello there" || len(resp.Words) != 1 || resp.DurationSeconds == nil || *resp.DurationSeconds != 1.5 {
		t.Fatalf("response %+v", resp)
	}
}

func TestCompatibleProviderHasNoAudio(t *testing.T) {
	p, err := NewCompatibleProvider("ollama", "Ollama", "http://localhost:11434/v1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Synthesize(context.Background(), inference.SpeechRequest{Model: "m", Input: "hi", Voice: "v"}); !errors.Is(err, inference.ErrUnsupported) {
		t.Fatalf("synthesize: %v", err)
	}
	info, _ := p.Info(context.Background())
	for _, c := range info.Capabilities {
		if c == inference.CapabilitySynthesize || c == inference.CapabilityTranscribe {
			t.Fatalf("compatible provider claims %s", c)
		}
	}
}

// The response the Cymonkey session ran the adapter against: tool calls with "content": null.
func TestToolCallWithNullContentValidates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"c2","model":"m1","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{\"key\":\"a\"}"}}]}}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}))
	defer server.Close()
	p, err := NewProvider(server.URL+"/v1", "secret")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Generate(context.Background(), userRequest())
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.Parts) != 0 {
		t.Fatalf("null content became parts: %+v", resp.Message.Parts)
	}
	if len(resp.Message.ToolCalls) != 1 || string(resp.Message.ToolCalls[0].Arguments) != `{"key":"a"}` {
		t.Fatalf("tool calls %+v", resp.Message.ToolCalls)
	}
	if err := resp.Validate(); err != nil {
		t.Fatalf("response does not validate: %v", err)
	}
}

func TestTokenInputsAndUnknownFinish(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/embeddings" {
			var body struct {
				Input [][]int64 `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Input) != 2 || body.Input[1][0] != 7 {
				t.Errorf("token input %v %v", body.Input, err)
			}
			_, _ = w.Write([]byte(`{"model":"e","data":[{"index":0,"embedding":[0.1]},{"index":1,"embedding":[0.2]}],"usage":{"prompt_tokens":3,"total_tokens":3}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"1","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"x"},"finish_reason":"insufficient_system_resource"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()
	p, err := NewProvider(server.URL+"/v1", "secret")
	if err != nil {
		t.Fatal(err)
	}
	emb, err := p.Embed(context.Background(), inference.EmbeddingRequest{Model: "e", TokenInputs: [][]int64{{1, 2}, {7}}})
	if err != nil || len(emb.Embeddings) != 2 {
		t.Fatalf("embed %+v %v", emb, err)
	}
	resp, err := p.Generate(context.Background(), userRequest())
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != inference.FinishReasonOther || resp.ProviderStopReason != "insufficient_system_resource" {
		t.Fatalf("got %s/%q", resp.StopReason, resp.ProviderStopReason)
	}
}
