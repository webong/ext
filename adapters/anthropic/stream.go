package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/webong/ext/res/int/inference"
)

// streamEvent is one server-sent event of a streamed Messages API response.
type streamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		ID    string   `json:"id"`
		Model string   `json:"model"`
		Usage apiUsage `json:"usage"`
	} `json:"message"`
	ContentBlock json.RawMessage `json:"content_block"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *apiUsage `json:"usage"`
	Error *struct {
		Type string `json:"type"`
	} `json:"error"`
}

// openBlock is a content block while its deltas arrive.
type openBlock struct {
	b    block
	raw  json.RawMessage
	text strings.Builder
	args strings.Builder
}

// Stream runs a generation with the Messages API's server-sent events. It emits started,
// text, reasoning and tool deltas, tool.completed for each tool call, usage, and a completed
// event carrying the final response. A stream that ends before message_stop fails.
func (p *provider) Stream(ctx context.Context, r inference.GenerationRequest, sink inference.EventSink) (inference.GenerationResponse, error) {
	body, err := p.payload(r, true)
	if err != nil {
		return inference.GenerationResponse{}, err
	}
	reply, err := p.send(ctx, http.MethodPost, "/messages", body, "text/event-stream")
	if err != nil {
		return inference.GenerationResponse{}, err
	}
	defer reply.Body.Close()
	stop := context.AfterFunc(ctx, func() { _ = reply.Body.Close() })
	defer stop()

	emit := func(ev inference.GenerationEvent) error {
		if err := sink.Emit(ctx, ev); err != nil {
			return inference.NewRemoteError(inference.ErrCanceled, "stream canceled by sink", "anthropic")
		}
		return nil
	}
	fail := func() (inference.GenerationResponse, error) {
		if ctx.Err() != nil {
			return inference.GenerationResponse{}, inference.NewRemoteError(inference.ErrCanceled, "operation canceled", "anthropic")
		}
		return inference.GenerationResponse{}, inference.NewRemoteError(inference.ErrInvalidResponse, "malformed stream", "anthropic")
	}

	out := inference.GenerationResponse{Message: inference.Message{Role: inference.RoleAssistant}}
	var usage apiUsage
	blocks := map[int]*openBlock{}
	var order []int
	stopped := false
	reader := bufio.NewReaderSize(io.LimitReader(reply.Body, (32<<20)+1), 64<<10)
	for !stopped {
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return fail()
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var ev streamEvent
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &ev); err != nil {
			return fail()
		}
		switch ev.Type {
		case "message_start":
			out.ID, out.Model, usage = ev.Message.ID, ev.Message.Model, ev.Message.Usage
			if err := emit(inference.GenerationEvent{Kind: inference.EventKindStarted}); err != nil {
				return inference.GenerationResponse{}, err
			}
		case "content_block_start":
			if _, ok := blocks[ev.Index]; ok || ev.Index < 0 || ev.Index > 4096 {
				return fail()
			}
			ob := &openBlock{raw: ev.ContentBlock}
			if err := json.Unmarshal(ev.ContentBlock, &ob.b); err != nil {
				return fail()
			}
			ob.text.WriteString(ob.b.Text)
			ob.text.WriteString(ob.b.Thinking)
			blocks[ev.Index] = ob
			order = append(order, ev.Index)
		case "content_block_delta":
			ob, ok := blocks[ev.Index]
			if !ok {
				return fail()
			}
			var event inference.GenerationEvent
			switch ev.Delta.Type {
			case "text_delta":
				ob.text.WriteString(ev.Delta.Text)
				event = inference.GenerationEvent{Kind: inference.EventKindTextDelta, Index: ev.Index, Text: ev.Delta.Text}
			case "thinking_delta":
				ob.text.WriteString(ev.Delta.Thinking)
				event = inference.GenerationEvent{Kind: inference.EventKindReasoningDelta, Index: ev.Index, Text: ev.Delta.Thinking}
			case "signature_delta":
				ob.b.Signature += ev.Delta.Signature
				continue
			case "input_json_delta":
				ob.args.WriteString(ev.Delta.PartialJSON)
				event = inference.GenerationEvent{Kind: inference.EventKindToolDelta, Index: ev.Index, CallID: ob.b.ID, ToolName: ob.b.Name, ArgumentsDelta: ev.Delta.PartialJSON}
			default:
				continue // a delta kind this adapter does not know carries nothing it maps
			}
			if err := emit(event); err != nil {
				return inference.GenerationResponse{}, err
			}
		case "content_block_stop":
			ob, ok := blocks[ev.Index]
			if !ok {
				return fail()
			}
			if ob.b.Type == "tool_use" {
				args := ob.args.String()
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				if !json.Valid([]byte(args)) {
					return fail()
				}
				ob.b.Input = json.RawMessage(args)
				call := inference.ToolCall{ID: ob.b.ID, Name: ob.b.Name, Arguments: ob.b.Input}
				if err := emit(inference.GenerationEvent{Kind: inference.EventKindToolCompleted, Index: ev.Index, CallID: call.ID, ToolName: call.Name, Call: &call}); err != nil {
					return inference.GenerationResponse{}, err
				}
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				if err := setStop(&out, ev.Delta.StopReason); err != nil {
					return inference.GenerationResponse{}, err
				}
			}
			if ev.Usage != nil {
				usage.Output = ev.Usage.Output
				u := usage.usage()
				if err := emit(inference.GenerationEvent{Kind: inference.EventKindUsage, Usage: &u}); err != nil {
					return inference.GenerationResponse{}, err
				}
			}
		case "message_stop":
			stopped = true
		case "error":
			code := inference.ErrUnavailable
			if ev.Error != nil && ev.Error.Type == "rate_limit_error" {
				code = inference.ErrRateLimit
			}
			return inference.GenerationResponse{}, inference.NewRemoteError(code, "provider stream failed", "anthropic")
		}
	}
	for _, i := range order {
		ob := blocks[i]
		switch ob.b.Type {
		case "text":
			ob.b.Text = ob.text.String()
		case "thinking":
			ob.b.Thinking = ob.text.String()
		}
		if err := appendBlock(&out.Message, ob.b, ob.raw); err != nil {
			return inference.GenerationResponse{}, err
		}
	}
	if out.Model == "" || out.StopReason == "" {
		return fail()
	}
	out.Usage = usage.usage()
	if err := out.Validate(); err != nil {
		return inference.GenerationResponse{}, err
	}
	done := out
	if err := emit(inference.GenerationEvent{Kind: inference.EventKindCompleted, Response: &done}); err != nil {
		return inference.GenerationResponse{}, err
	}
	return out, nil
}

// ListModels lists the models the API key can use.
func (p *provider) ListModels(ctx context.Context) ([]inference.ModelInfo, error) {
	reply, err := p.send(ctx, http.MethodGet, "/models?limit=1000", nil, "application/json")
	if err != nil {
		return nil, err
	}
	defer reply.Body.Close()
	var result struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(reply.Body, 8<<20)).Decode(&result); err != nil {
		return nil, inference.ErrInvalidResponse
	}
	models := make([]inference.ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, inference.ModelInfo{ID: m.ID, Name: m.DisplayName})
	}
	return models, nil
}
