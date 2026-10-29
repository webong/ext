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
	"regexp"
	"strings"
	"time"

	ginference "github.com/webong/ext/pkg/ext-go/inference"
	"github.com/webong/ext/res/inference"
)

var modelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,255}$`)

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
	return inference.ProviderInfo{ID: "gemini", Name: "Gemini", Version: "1.0.0", Stability: inference.StabilityStable, Modalities: []inference.Modality{inference.ModalityText}, Capabilities: []inference.Capability{inference.CapabilityGenerate}, Limits: inference.Limits{MaxBytes: 32 << 20}, Protocol: map[string]string{"endpoint": p.endpoint, "auth": "x-goog-api-key"}}, nil
}
func (p *provider) Close() error { return nil }
func (p *provider) Generate(ctx context.Context, r inference.GenerationRequest) (inference.GenerationResponse, error) {
	if err := r.Validate(); err != nil {
		return inference.GenerationResponse{}, err
	}
	if !modelName.MatchString(r.Model) {
		return inference.GenerationResponse{}, inference.ErrInvalidRequest
	}
	if len(r.Tools) > 0 || len(r.OutputSchema) > 0 || len(r.Options) > 0 {
		return inference.GenerationResponse{}, inference.ErrUnsupported
	}
	type part struct {
		Text string `json:"text"`
	}
	type content struct {
		Role  string `json:"role,omitempty"`
		Parts []part `json:"parts"`
	}
	var system []part
	var messages []content
	for _, m := range r.Messages {
		if len(m.ToolCalls) > 0 {
			return inference.GenerationResponse{}, inference.ErrUnsupported
		}
		parts := make([]part, 0, len(m.Parts))
		for _, p := range m.Parts {
			if p.Kind != inference.PartKindText {
				return inference.GenerationResponse{}, inference.ErrUnsupported
			}
			parts = append(parts, part{p.Text})
		}
		switch m.Role {
		case inference.RoleSystem:
			system = append(system, parts...)
		case inference.RoleUser:
			messages = append(messages, content{Role: "user", Parts: parts})
		case inference.RoleAssistant:
			messages = append(messages, content{Role: "model", Parts: parts})
		default:
			return inference.GenerationResponse{}, inference.ErrUnsupported
		}
	}
	if len(messages) == 0 {
		return inference.GenerationResponse{}, inference.ErrInvalidRequest
	}
	payload := struct {
		Contents []content `json:"contents"`
		System   *content  `json:"system_instruction,omitempty"`
		Config   struct {
			MaxTokens   int      `json:"maxOutputTokens,omitempty"`
			Temperature *float64 `json:"temperature,omitempty"`
		} `json:"generationConfig"`
	}{Contents: messages}
	if len(system) > 0 {
		payload.System = &content{Parts: system}
	}
	payload.Config.MaxTokens = r.MaxOutputTokens
	payload.Config.Temperature = r.Temperature
	body, err := json.Marshal(payload)
	if err != nil {
		return inference.GenerationResponse{}, err
	}
	endpoint := p.endpoint + "/models/" + r.Model + ":generateContent"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return inference.GenerationResponse{}, inference.ErrInvalidRequest
	}
	req.Header.Set("x-goog-api-key", p.key)
	req.Header.Set("content-type", "application/json")
	reply, err := p.client.Do(req)
	if err != nil {
		return inference.GenerationResponse{}, inference.NewRemoteError(inference.ErrUnavailable, "request failed", "gemini")
	}
	defer reply.Body.Close()
	if reply.StatusCode < 200 || reply.StatusCode >= 300 {
		return inference.GenerationResponse{}, inference.NewRemoteError(statusError(reply.StatusCode), "provider request failed", "gemini")
	}
	raw, err := io.ReadAll(io.LimitReader(reply.Body, (32<<20)+1))
	if err != nil {
		return inference.GenerationResponse{}, inference.ErrInvalidResponse
	}
	if len(raw) > 32<<20 {
		return inference.GenerationResponse{}, inference.ErrResourceExhausted
	}
	var result struct {
		Candidates []struct {
			Content struct {
				Parts []map[string]json.RawMessage `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		Usage struct {
			Input  int64 `json:"promptTokenCount"`
			Output int64 `json:"candidatesTokenCount"`
			Total  int64 `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || len(result.Candidates) == 0 {
		return inference.GenerationResponse{}, inference.ErrInvalidResponse
	}
	first := result.Candidates[0]
	if len(first.Content.Parts) == 0 {
		return inference.GenerationResponse{}, inference.ErrInvalidResponse
	}
	out := inference.GenerationResponse{Model: r.Model, Message: inference.Message{Role: inference.RoleAssistant}, Usage: inference.TokenUsage{Input: result.Usage.Input, Output: result.Usage.Output, Total: result.Usage.Total}}
	for _, part := range first.Content.Parts {
		if len(part) != 1 {
			return inference.GenerationResponse{}, inference.ErrUnsupported
		}
		rawText, ok := part["text"]
		if !ok {
			return inference.GenerationResponse{}, inference.ErrUnsupported
		}
		var text string
		if err := json.Unmarshal(rawText, &text); err != nil {
			return inference.GenerationResponse{}, inference.ErrInvalidResponse
		}
		out.Message.Parts = append(out.Message.Parts, inference.Part{Kind: inference.PartKindText, Text: text})
	}
	switch first.FinishReason {
	case "STOP":
		out.StopReason = inference.FinishReasonStop
	case "MAX_TOKENS":
		out.StopReason = inference.FinishReasonLength
	case "SAFETY", "RECITATION":
		out.StopReason = inference.FinishReasonContentFilter
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
		fmt.Fprintln(os.Stderr, "gemini:", err)
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
		return ginference.ServeCurrentExecutable(context.Background(), "gemini", "1.0.0", p, func(ctx context.Context, _ ginference.Request) error { return ctx.Err() })
	default:
		return inference.ErrUnsupported
	}
}
