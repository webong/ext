package main

import (
	"bytes"
	"context"
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
	"github.com/webong/ext/res/inference"
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
	return inference.ProviderInfo{ID: "anthropic", Name: "Anthropic", Version: "1.0.0", Stability: inference.StabilityStable, Modalities: []inference.Modality{inference.ModalityText}, Capabilities: []inference.Capability{inference.CapabilityGenerate}, Limits: inference.Limits{MaxBytes: 32 << 20}, Protocol: map[string]string{"endpoint": p.endpoint, "auth": "x-api-key"}}, nil
}
func (p *provider) Close() error { return nil }

func (p *provider) Generate(ctx context.Context, r inference.GenerationRequest) (inference.GenerationResponse, error) {
	if err := r.Validate(); err != nil {
		return inference.GenerationResponse{}, err
	}
	if r.MaxOutputTokens <= 0 {
		return inference.GenerationResponse{}, inference.ErrInvalidRequest
	}
	if len(r.Tools) > 0 || len(r.OutputSchema) > 0 || len(r.Options) > 0 {
		return inference.GenerationResponse{}, inference.ErrUnsupported
	}
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var system []string
	messages := make([]msg, 0, len(r.Messages))
	for _, m := range r.Messages {
		if len(m.ToolCalls) > 0 {
			return inference.GenerationResponse{}, inference.ErrUnsupported
		}
		var text strings.Builder
		for _, part := range m.Parts {
			if part.Kind != inference.PartKindText {
				return inference.GenerationResponse{}, inference.ErrUnsupported
			}
			text.WriteString(part.Text)
		}
		switch m.Role {
		case inference.RoleSystem:
			system = append(system, text.String())
		case inference.RoleUser, inference.RoleAssistant:
			messages = append(messages, msg{Role: string(m.Role), Content: text.String()})
		default:
			return inference.GenerationResponse{}, inference.ErrUnsupported
		}
	}
	if len(messages) == 0 {
		return inference.GenerationResponse{}, inference.ErrInvalidRequest
	}
	payload := struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		System    string `json:"system,omitempty"`
		Messages  []msg  `json:"messages"`
	}{r.Model, r.MaxOutputTokens, strings.Join(system, "\n"), messages}
	body, err := json.Marshal(payload)
	if err != nil {
		return inference.GenerationResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/messages", bytes.NewReader(body))
	if err != nil {
		return inference.GenerationResponse{}, inference.ErrInvalidRequest
	}
	req.Header.Set("x-api-key", p.key)
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
			Type string `json:"type"`
			Text string `json:"text"`
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
		if part.Type != "text" {
			return inference.GenerationResponse{}, inference.ErrUnsupported
		}
		out.Message.Parts = append(out.Message.Parts, inference.Part{Kind: inference.PartKindText, Text: part.Text})
	}
	switch result.StopReason {
	case "end_turn", "stop_sequence":
		out.StopReason = inference.FinishReasonStop
	case "max_tokens":
		out.StopReason = inference.FinishReasonLength
	case "refusal":
		out.StopReason = inference.FinishReasonRefusal
	default:
		return inference.GenerationResponse{}, inference.ErrInvalidResponse
	}
	return out, out.Validate()
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
