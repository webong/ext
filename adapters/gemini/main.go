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
	"regexp"
	"strings"
	"time"

	ginference "github.com/webong/ext/pkg/ext-go/inference"
	"github.com/webong/ext/res/int/inference"
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
	return inference.ProviderInfo{ID: "gemini", Name: "Gemini", Version: "1.0.0", Stability: inference.StabilityStable, Modalities: []inference.Modality{inference.ModalityText, inference.ModalityImage}, Capabilities: []inference.Capability{inference.CapabilityGenerate, inference.CapabilityToolProposal, inference.CapabilityStructuredOutput}, Limits: inference.Limits{MaxBytes: 32 << 20}, Protocol: map[string]string{"endpoint": p.endpoint, "auth": "x-goog-api-key"}}, nil
}
func (p *provider) Close() error { return nil }
func (p *provider) Generate(ctx context.Context, r inference.GenerationRequest) (inference.GenerationResponse, error) {
	if err := r.Validate(); err != nil {
		return inference.GenerationResponse{}, err
	}
	if !modelName.MatchString(r.Model) {
		return inference.GenerationResponse{}, inference.ErrInvalidRequest
	}
	if len(r.Options) > 0 {
		return inference.GenerationResponse{}, inference.ErrUnsupported
	}
	type content struct {
		Role  string           `json:"role,omitempty"`
		Parts []map[string]any `json:"parts"`
	}
	var system []map[string]any
	var messages []content
	for _, m := range r.Messages {
		parts := make([]map[string]any, 0, len(m.Parts)+len(m.ToolCalls))
		for _, part := range m.Parts {
			switch part.Kind {
			case inference.PartKindText:
				parts = append(parts, map[string]any{"text": part.Text})
			case inference.PartKindImage:
				if m.Role != inference.RoleUser || part.Reference != nil || !supportedImage(part.MediaType) {
					return inference.GenerationResponse{}, inference.ErrUnsupported
				}
				parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": part.MediaType, "data": base64.StdEncoding.EncodeToString(part.Data)}})
			default:
				return inference.GenerationResponse{}, inference.ErrUnsupported
			}
		}
		switch m.Role {
		case inference.RoleSystem:
			for _, part := range parts {
				if _, ok := part["text"]; !ok {
					return inference.GenerationResponse{}, inference.ErrUnsupported
				}
			}
			system = append(system, parts...)
		case inference.RoleUser:
			messages = append(messages, content{Role: "user", Parts: parts})
		case inference.RoleAssistant:
			for _, call := range m.ToolCalls {
				function := map[string]any{"name": call.Name, "args": call.Arguments}
				if !strings.HasPrefix(call.ID, "gemini-call-") {
					function["id"] = call.ID
				}
				parts = append(parts, map[string]any{"functionCall": function})
			}
			messages = append(messages, content{Role: "model", Parts: parts})
		case inference.RoleTool:
			if m.ToolName == "" {
				return inference.GenerationResponse{}, inference.ErrInvalidRequest
			}
			for _, part := range m.Parts {
				if part.Kind != inference.PartKindText {
					return inference.GenerationResponse{}, inference.ErrUnsupported
				}
			}
			response := map[string]any{"name": m.ToolName, "response": map[string]any{"result": textParts(m.Parts)}}
			if !strings.HasPrefix(m.ToolCallID, "gemini-call-") {
				response["id"] = m.ToolCallID
			}
			if m.ToolError {
				response["response"] = map[string]any{"error": textParts(m.Parts)}
			}
			messages = append(messages, content{Role: "user", Parts: []map[string]any{{"functionResponse": response}}})
		default:
			return inference.GenerationResponse{}, inference.ErrUnsupported
		}
	}
	if len(messages) == 0 {
		return inference.GenerationResponse{}, inference.ErrInvalidRequest
	}
	payload := map[string]any{"contents": messages}
	if len(system) > 0 {
		payload["systemInstruction"] = content{Parts: system}
	}
	config := map[string]any{}
	if r.MaxOutputTokens > 0 {
		config["maxOutputTokens"] = r.MaxOutputTokens
	}
	if r.Temperature != nil {
		config["temperature"] = *r.Temperature
	}
	if len(r.OutputSchema) > 0 {
		config["responseMimeType"] = "application/json"
		config["responseJsonSchema"] = r.OutputSchema
	}
	if len(config) > 0 {
		payload["generationConfig"] = config
	}
	if len(r.Tools) > 0 {
		declarations := make([]map[string]any, 0, len(r.Tools))
		for _, tool := range r.Tools {
			declarations = append(declarations, map[string]any{"name": tool.Name, "description": tool.Description, "parametersJsonSchema": tool.InputSchema})
		}
		payload["tools"] = []map[string]any{{"functionDeclarations": declarations}}
	}
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
		if rawText, ok := part["text"]; ok {
			var text string
			if err := json.Unmarshal(rawText, &text); err != nil {
				return inference.GenerationResponse{}, inference.ErrInvalidResponse
			}
			out.Message.Parts = append(out.Message.Parts, inference.Part{Kind: inference.PartKindText, Text: text})
		} else if rawCall, ok := part["functionCall"]; ok {
			var call struct {
				ID   string          `json:"id"`
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			}
			if err := json.Unmarshal(rawCall, &call); err != nil {
				return inference.GenerationResponse{}, inference.ErrInvalidResponse
			}
			if call.ID == "" {
				call.ID = fmt.Sprintf("gemini-call-%d", len(out.Message.ToolCalls))
			}
			if len(call.Args) == 0 {
				call.Args = json.RawMessage(`{}`)
			}
			out.Message.ToolCalls = append(out.Message.ToolCalls, inference.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Args})
		} else {
			return inference.GenerationResponse{}, inference.ErrUnsupported
		}
	}
	switch first.FinishReason {
	case "STOP":
		if len(out.Message.ToolCalls) > 0 {
			out.StopReason = inference.FinishReasonToolCalls
		} else {
			out.StopReason = inference.FinishReasonStop
		}
	case "MAX_TOKENS":
		out.StopReason = inference.FinishReasonLength
	case "SAFETY", "RECITATION":
		out.StopReason = inference.FinishReasonContentFilter
	default:
		return inference.GenerationResponse{}, inference.ErrInvalidResponse
	}
	return out, out.Validate()
}
func textParts(parts []inference.Part) string {
	var text strings.Builder
	for _, part := range parts {
		text.WriteString(part.Text)
	}
	return text.String()
}
func supportedImage(mediaType string) bool {
	switch mediaType {
	case "image/png", "image/jpeg", "image/jpg", "image/webp", "image/heic", "image/heif", "image/gif", "image/avif":
		return true
	default:
		return false
	}
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
