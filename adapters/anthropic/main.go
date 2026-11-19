package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	ginference "github.com/webong/ext/pkg/ext-go/inference"
	"github.com/webong/ext/res/int/inference"
)

type provider struct {
	endpoint, key string
	client        *http.Client
}

func newProvider(endpoint, key string) (*provider, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || key == "" {
		return nil, inference.ErrInvalidRequest
	}
	return &provider{endpoint: strings.TrimRight(endpoint, "/"), key: key, client: &http.Client{Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (p *provider) Info(ctx context.Context) (inference.ProviderInfo, error) {
	if err := ctx.Err(); err != nil {
		return inference.ProviderInfo{}, err
	}
	return inference.ProviderInfo{ID: "anthropic", Name: "Anthropic", Version: "1.0.0", Stability: inference.StabilityStable, Modalities: []inference.Modality{inference.ModalityText, inference.ModalityImage}, Capabilities: []inference.Capability{inference.CapabilityGenerate, inference.CapabilityStream, inference.CapabilityToolProposal, inference.CapabilityStructuredOutput, inference.CapabilityModelListing}, Limits: inference.Limits{MaxBytes: 32 << 20}, Protocol: map[string]string{"endpoint": p.endpoint, "auth": "x-api-key"}}, nil
}
func (p *provider) Close() error { return nil }

// optionKeys are the request options passed through to the Messages API as they are;
// any other key is refused rather than dropped.
var optionKeys = map[string]bool{
	"top_k": true, "top_p": true, "stop_sequences": true, "metadata": true,
	"thinking": true, "tool_choice": true, "service_tier": true,
}

// payload builds a Messages API request body.
func (p *provider) payload(r inference.GenerationRequest, stream bool) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if r.MaxOutputTokens <= 0 {
		return nil, inference.ErrInvalidRequest
	}
	type msg struct {
		Role    string           `json:"role"`
		Content []map[string]any `json:"content"`
	}
	var system []string
	messages := make([]msg, 0, len(r.Messages))
	for _, m := range r.Messages {
		content := make([]map[string]any, 0, len(m.Parts)+len(m.ToolCalls))
		for _, part := range m.Parts {
			switch part.Kind {
			case inference.PartKindText:
				content = append(content, map[string]any{"type": "text", "text": part.Text})
			case inference.PartKindImage:
				if m.Role != inference.RoleUser || part.Reference != nil || !supportedImage(part.MediaType) {
					return nil, inference.ErrUnsupported
				}
				content = append(content, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": part.MediaType, "data": base64.StdEncoding.EncodeToString(part.Data)}})
			case inference.PartKindReasoning:
				// Extended thinking goes back with the signature it came with.
				block := map[string]any{"type": "thinking", "thinking": part.Text}
				var origin struct {
					Signature string `json:"signature"`
				}
				if len(part.ProviderData) > 0 && json.Unmarshal(part.ProviderData, &origin) == nil && origin.Signature != "" {
					block["signature"] = origin.Signature
				}
				content = append(content, block)
			case inference.PartKindContext:
				// An opaque block (redacted thinking) goes back exactly as it came.
				var block map[string]any
				if err := json.Unmarshal(part.ProviderData, &block); err != nil || block["type"] == nil {
					return nil, inference.ErrInvalidRequest
				}
				content = append(content, block)
			default:
				return nil, inference.ErrUnsupported
			}
		}
		switch m.Role {
		case inference.RoleSystem:
			for _, part := range content {
				if part["type"] != "text" {
					return nil, inference.ErrUnsupported
				}
				system = append(system, part["text"].(string))
			}
		case inference.RoleUser, inference.RoleAssistant:
			for _, call := range m.ToolCalls {
				input := call.Arguments
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				content = append(content, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": input})
			}
			messages = append(messages, msg{Role: string(m.Role), Content: content})
		case inference.RoleTool:
			for _, part := range m.Parts {
				if part.Kind != inference.PartKindText {
					return nil, inference.ErrUnsupported
				}
			}
			result := map[string]any{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": textParts(m.Parts)}
			if m.ToolError {
				result["is_error"] = true
			}
			messages = append(messages, msg{Role: "user", Content: []map[string]any{result}})
		default:
			return nil, inference.ErrUnsupported
		}
	}
	if len(messages) == 0 {
		return nil, inference.ErrInvalidRequest
	}
	payload := map[string]any{"model": r.Model, "max_tokens": r.MaxOutputTokens, "messages": messages}
	if len(r.Options) > 0 {
		var options map[string]json.RawMessage
		if err := json.Unmarshal(r.Options, &options); err != nil {
			return nil, inference.ErrInvalidRequest
		}
		for key, value := range options {
			if !optionKeys[key] {
				return nil, fmt.Errorf("%w: option %q", inference.ErrUnsupported, key)
			}
			payload[key] = value
		}
	}
	if len(system) > 0 {
		payload["system"] = strings.Join(system, "\n")
	}
	if r.Temperature != nil {
		payload["temperature"] = *r.Temperature
	}
	if len(r.Tools) > 0 {
		tools := make([]map[string]any, 0, len(r.Tools))
		for _, tool := range r.Tools {
			tools = append(tools, map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": tool.InputSchema})
		}
		payload["tools"] = tools
	}
	if len(r.OutputSchema) > 0 {
		payload["output_config"] = map[string]any{"format": map[string]any{"type": "json_schema", "schema": r.OutputSchema}}
	}
	if stream {
		payload["stream"] = true
	}
	return json.Marshal(payload)
}

// send posts a request to the API and returns the reply when its status is a success.
func (p *provider) send(ctx context.Context, method, path string, body []byte, accept string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.endpoint+path, reader)
	if err != nil {
		return nil, inference.ErrInvalidRequest
	}
	req.Header.Set("x-api-key", p.key)
	// The newer 2026-10-21 version removes temperature; keep the compatible
	// version while the neutral generation contract still exposes it.
	req.Header.Set("anthropic-version", "2023-06-01")
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	if accept != "" {
		req.Header.Set("accept", accept)
	}
	reply, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, inference.NewRemoteError(inference.ErrCanceled, "operation canceled", "anthropic")
		}
		return nil, inference.NewRemoteError(inference.ErrUnavailable, "request failed", "anthropic")
	}
	if reply.StatusCode < 200 || reply.StatusCode >= 300 {
		reply.Body.Close()
		return nil, inference.NewRemoteError(statusError(reply.StatusCode), "provider request failed", "anthropic")
	}
	return reply, nil
}

// block is one content block of a Messages API response.
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	Data      string          `json:"data"`
}

// apiUsage is the Messages API's token usage.
type apiUsage struct {
	Input       int64 `json:"input_tokens"`
	Output      int64 `json:"output_tokens"`
	CacheRead   int64 `json:"cache_read_input_tokens"`
	CacheCreate int64 `json:"cache_creation_input_tokens"`
}

func (u apiUsage) usage() inference.TokenUsage {
	input := u.Input + u.CacheRead + u.CacheCreate
	return inference.TokenUsage{Input: input, Output: u.Output, Total: input + u.Output, CachedInput: u.CacheRead}
}

// appendBlock adds a response block to the message. raw is the block as the API sent it,
// kept for opaque blocks that must go back unchanged.
func appendBlock(m *inference.Message, b block, raw json.RawMessage) error {
	switch b.Type {
	case "text":
		m.Parts = append(m.Parts, inference.Part{Kind: inference.PartKindText, Text: b.Text})
	case "tool_use":
		input := b.Input
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		m.ToolCalls = append(m.ToolCalls, inference.ToolCall{ID: b.ID, Name: b.Name, Arguments: input})
	case "thinking":
		origin, _ := json.Marshal(map[string]string{"signature": b.Signature})
		m.Parts = append(m.Parts, inference.Part{Kind: inference.PartKindReasoning, Text: b.Thinking, ProviderData: origin})
	case "redacted_thinking":
		m.Parts = append(m.Parts, inference.Part{Kind: inference.PartKindContext, ProviderData: append(json.RawMessage(nil), raw...)})
	default:
		return inference.ErrUnsupported
	}
	return nil
}

func stopReason(s string) (inference.FinishReason, error) {
	switch s {
	case "end_turn", "stop_sequence":
		return inference.FinishReasonStop, nil
	case "max_tokens":
		return inference.FinishReasonLength, nil
	case "tool_use":
		return inference.FinishReasonToolCalls, nil
	case "refusal":
		return inference.FinishReasonRefusal, nil
	default:
		return "", inference.ErrInvalidResponse
	}
}

func (p *provider) Generate(ctx context.Context, r inference.GenerationRequest) (inference.GenerationResponse, error) {
	body, err := p.payload(r, false)
	if err != nil {
		return inference.GenerationResponse{}, err
	}
	reply, err := p.send(ctx, http.MethodPost, "/messages", body, "")
	if err != nil {
		return inference.GenerationResponse{}, err
	}
	defer reply.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(reply.Body, (32<<20)+1))
	if err != nil {
		return inference.GenerationResponse{}, inference.ErrInvalidResponse
	}
	if len(raw) > 32<<20 {
		return inference.GenerationResponse{}, inference.ErrResourceExhausted
	}
	var result struct {
		ID         string            `json:"id"`
		Model      string            `json:"model"`
		Content    []json.RawMessage `json:"content"`
		StopReason string            `json:"stop_reason"`
		Usage      apiUsage          `json:"usage"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Model == "" || len(result.Content) == 0 {
		return inference.GenerationResponse{}, inference.ErrInvalidResponse
	}
	out := inference.GenerationResponse{ID: result.ID, Model: result.Model, Message: inference.Message{Role: inference.RoleAssistant}, Usage: result.Usage.usage()}
	for _, rawBlock := range result.Content {
		var b block
		if err := json.Unmarshal(rawBlock, &b); err != nil {
			return inference.GenerationResponse{}, inference.ErrInvalidResponse
		}
		if err := appendBlock(&out.Message, b, rawBlock); err != nil {
			return inference.GenerationResponse{}, err
		}
	}
	if out.StopReason, err = stopReason(result.StopReason); err != nil {
		return inference.GenerationResponse{}, err
	}
	return out, out.Validate()
}

func supportedImage(mediaType string) bool {
	switch mediaType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}
func textParts(parts []inference.Part) string {
	var text strings.Builder
	for _, part := range parts {
		if part.Kind == inference.PartKindText {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}
func statusError(code int) error {
	switch code {
	case 401:
		return inference.ErrAuthentication
	case 403:
		return inference.ErrAuthorization
	case 429:
		return inference.ErrRateLimit
	}
	if code >= 500 {
		return inference.ErrUnavailable
	}
	return inference.ErrInvalidRequest
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "anthropic:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) != 1 {
		return errors.New("operation required")
	}
	switch args[0] {
	case "validate", "doctor":
		return nil
	case "run":
		p, err := newProvider(os.Getenv("EXT_ADAPTER_VALUE_endpoint"), os.Getenv("EXT_ADAPTER_VALUE_api_key"))
		if err != nil {
			return err
		}
		defer p.Close()
		return ginference.ServeCurrentExecutable(context.Background(), "anthropic", "1.0.0", p, func(ctx context.Context, _ ginference.Request) error { return ctx.Err() })
	default:
		return inference.ErrUnsupported
	}
}
