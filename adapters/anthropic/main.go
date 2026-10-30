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
	return inference.ProviderInfo{ID: "anthropic", Name: "Anthropic", Version: "1.0.0", Stability: inference.StabilityStable, Modalities: []inference.Modality{inference.ModalityText, inference.ModalityImage}, Capabilities: []inference.Capability{inference.CapabilityGenerate, inference.CapabilityToolProposal, inference.CapabilityStructuredOutput}, Limits: inference.Limits{MaxBytes: 32 << 20}, Protocol: map[string]string{"endpoint": p.endpoint, "auth": "x-api-key"}}, nil
}
func (p *provider) Close() error { return nil }

func (p *provider) Generate(ctx context.Context, r inference.GenerationRequest) (inference.GenerationResponse, error) {
	if err := r.Validate(); err != nil {
		return inference.GenerationResponse{}, err
	}
	if r.MaxOutputTokens <= 0 {
		return inference.GenerationResponse{}, inference.ErrInvalidRequest
	}
	if len(r.Options) > 0 {
		return inference.GenerationResponse{}, inference.ErrUnsupported
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
					return inference.GenerationResponse{}, inference.ErrUnsupported
				}
				content = append(content, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": part.MediaType, "data": base64.StdEncoding.EncodeToString(part.Data)}})
			default:
				return inference.GenerationResponse{}, inference.ErrUnsupported
			}
		}
		switch m.Role {
		case inference.RoleSystem:
			for _, part := range content {
				if part["type"] != "text" {
					return inference.GenerationResponse{}, inference.ErrUnsupported
				}
				system = append(system, part["text"].(string))
			}
		case inference.RoleUser, inference.RoleAssistant:
			for _, call := range m.ToolCalls {
				content = append(content, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": call.Arguments})
			}
			messages = append(messages, msg{Role: string(m.Role), Content: content})
		case inference.RoleTool:
			for _, part := range m.Parts {
				if part.Kind != inference.PartKindText {
					return inference.GenerationResponse{}, inference.ErrUnsupported
				}
			}
			if m.ToolError {
				content = []map[string]any{{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": textParts(m.Parts), "is_error": true}}
			} else {
				content = []map[string]any{{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": textParts(m.Parts)}}
			}
			messages = append(messages, msg{Role: "user", Content: content})
		default:
			return inference.GenerationResponse{}, inference.ErrUnsupported
		}
	}
	if len(messages) == 0 {
		return inference.GenerationResponse{}, inference.ErrInvalidRequest
	}
	payload := map[string]any{"model": r.Model, "max_tokens": r.MaxOutputTokens, "messages": messages}
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
	body, err := json.Marshal(payload)
	if err != nil {
		return inference.GenerationResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/messages", bytes.NewReader(body))
	if err != nil {
		return inference.GenerationResponse{}, inference.ErrInvalidRequest
	}
	req.Header.Set("x-api-key", p.key)
	// The newer 2026-10-21 version removes temperature; keep the compatible
	// version while the neutral generation contract still exposes it.
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	reply, err := p.client.Do(req)
	if err != nil {
		return inference.GenerationResponse{}, inference.NewRemoteError(inference.ErrUnavailable, "request failed", "anthropic")
	}
	defer reply.Body.Close()
	if reply.StatusCode < 200 || reply.StatusCode >= 300 {
		return inference.GenerationResponse{}, inference.NewRemoteError(statusError(reply.StatusCode), "provider request failed", "anthropic")
	}
	raw, err := io.ReadAll(io.LimitReader(reply.Body, (32<<20)+1))
	if err != nil {
		return inference.GenerationResponse{}, inference.ErrInvalidResponse
	}
	if len(raw) > 32<<20 {
		return inference.GenerationResponse{}, inference.ErrResourceExhausted
	}
	var result struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			Input  int64 `json:"input_tokens"`
			Output int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Model == "" || len(result.Content) == 0 {
		return inference.GenerationResponse{}, inference.ErrInvalidResponse
	}
	out := inference.GenerationResponse{ID: result.ID, Model: result.Model, Message: inference.Message{Role: inference.RoleAssistant}, Usage: inference.TokenUsage{Input: result.Usage.Input, Output: result.Usage.Output, Total: result.Usage.Input + result.Usage.Output}}
	for _, part := range result.Content {
		switch part.Type {
		case "text":
			out.Message.Parts = append(out.Message.Parts, inference.Part{Kind: inference.PartKindText, Text: part.Text})
		case "tool_use":
			out.Message.ToolCalls = append(out.Message.ToolCalls, inference.ToolCall{ID: part.ID, Name: part.Name, Arguments: part.Input})
		default:
			return inference.GenerationResponse{}, inference.ErrUnsupported
		}
	}
	switch result.StopReason {
	case "end_turn", "stop_sequence":
		out.StopReason = inference.FinishReasonStop
	case "max_tokens":
		out.StopReason = inference.FinishReasonLength
	case "tool_use":
		out.StopReason = inference.FinishReasonToolCalls
	case "refusal":
		out.StopReason = inference.FinishReasonRefusal
	default:
		return inference.GenerationResponse{}, inference.ErrInvalidResponse
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
