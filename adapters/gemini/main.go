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
	return inference.ProviderInfo{ID: "gemini", Name: "Gemini", Version: "1.0.0", Stability: inference.StabilityStable, Modalities: []inference.Modality{inference.ModalityText, inference.ModalityImage}, Capabilities: []inference.Capability{inference.CapabilityGenerate, inference.CapabilityStream, inference.CapabilityEmbed, inference.CapabilityToolProposal, inference.CapabilityStructuredOutput, inference.CapabilityModelListing}, Limits: inference.Limits{MaxBytes: 32 << 20}, Protocol: map[string]string{"endpoint": p.endpoint, "auth": "x-goog-api-key"}}, nil
}
func (p *provider) Close() error { return nil }
// generationOptions go into generationConfig and topOptions beside it; any other request
// option is refused rather than dropped.
var (
	generationOptions = map[string]bool{
		"topK": true, "topP": true, "stopSequences": true, "thinkingConfig": true, "seed": true,
		"presencePenalty": true, "frequencyPenalty": true, "responseLogprobs": true, "logprobs": true,
	}
	topOptions = map[string]bool{"safetySettings": true, "toolConfig": true, "cachedContent": true}
)

// origin is what Gemini needs back from an earlier turn: the signature of a thought,
// text or function call part.
type origin struct {
	ThoughtSignature string `json:"thoughtSignature,omitempty"`
}

func signature(data json.RawMessage) string {
	var o origin
	if len(data) > 0 && json.Unmarshal(data, &o) == nil {
		return o.ThoughtSignature
	}
	return ""
}

// payload builds a generateContent request body.
func (p *provider) payload(r inference.GenerationRequest) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if !modelName.MatchString(r.Model) {
		return nil, inference.ErrInvalidRequest
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
			var out map[string]any
			switch part.Kind {
			case inference.PartKindText:
				out = map[string]any{"text": part.Text}
			case inference.PartKindReasoning:
				out = map[string]any{"text": part.Text, "thought": true}
			case inference.PartKindImage:
				if m.Role != inference.RoleUser || part.Reference != nil || !supportedImage(part.MediaType) {
					return nil, inference.ErrUnsupported
				}
				out = map[string]any{"inlineData": map[string]any{"mimeType": part.MediaType, "data": base64.StdEncoding.EncodeToString(part.Data)}}
			default:
				return nil, inference.ErrUnsupported
			}
			if sig := signature(part.ProviderData); sig != "" {
				out["thoughtSignature"] = sig
			}
			parts = append(parts, out)
		}
		switch m.Role {
		case inference.RoleSystem:
			for _, part := range parts {
				if _, ok := part["text"]; !ok || part["thought"] != nil {
					return nil, inference.ErrUnsupported
				}
			}
			system = append(system, parts...)
		case inference.RoleUser:
			messages = append(messages, content{Role: "user", Parts: parts})
		case inference.RoleAssistant:
			for _, call := range m.ToolCalls {
				args := call.Arguments
				if len(args) == 0 {
					args = json.RawMessage(`{}`)
				}
				function := map[string]any{"name": call.Name, "args": args}
				if !strings.HasPrefix(call.ID, "gemini-call-") {
					function["id"] = call.ID
				}
				out := map[string]any{"functionCall": function}
				if sig := signature(call.ProviderData); sig != "" {
					out["thoughtSignature"] = sig
				}
				parts = append(parts, out)
			}
			messages = append(messages, content{Role: "model", Parts: parts})
		case inference.RoleTool:
			if m.ToolName == "" {
				return nil, inference.ErrInvalidRequest
			}
			for _, part := range m.Parts {
				if part.Kind != inference.PartKindText {
					return nil, inference.ErrUnsupported
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
			return nil, inference.ErrUnsupported
		}
	}
	if len(messages) == 0 {
		return nil, inference.ErrInvalidRequest
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
	if len(r.Options) > 0 {
		var options map[string]json.RawMessage
		if err := json.Unmarshal(r.Options, &options); err != nil {
			return nil, inference.ErrInvalidRequest
		}
		for key, value := range options {
			switch {
			case generationOptions[key]:
				config[key] = value
			case topOptions[key]:
				payload[key] = value
			default:
				return nil, fmt.Errorf("%w: option %q", inference.ErrUnsupported, key)
			}
		}
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
	return json.Marshal(payload)
}

// send posts to the API and returns the reply when its status is a success.
func (p *provider) send(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.endpoint+path, reader)
	if err != nil {
		return nil, inference.ErrInvalidRequest
	}
	req.Header.Set("x-goog-api-key", p.key)
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	reply, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, inference.NewRemoteError(inference.ErrCanceled, "operation canceled", "gemini")
		}
		return nil, inference.NewRemoteError(inference.ErrUnavailable, "request failed", "gemini")
	}
	if reply.StatusCode < 200 || reply.StatusCode >= 300 {
		reply.Body.Close()
		return nil, inference.NewRemoteError(statusError(reply.StatusCode), "provider request failed", "gemini")
	}
	return reply, nil
}

// apiPart is one part of a candidate's content.
type apiPart struct {
	Text             *string `json:"text"`
	Thought          bool    `json:"thought"`
	ThoughtSignature string  `json:"thoughtSignature"`
	FunctionCall     *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
}

// chunk is a generateContent response, or one event of a streamed one.
type chunk struct {
	Candidates []struct {
		Content struct {
			Parts []apiPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	Usage *struct {
		Input    int64 `json:"promptTokenCount"`
		Output   int64 `json:"candidatesTokenCount"`
		Thoughts int64 `json:"thoughtsTokenCount"`
		Cached   int64 `json:"cachedContentTokenCount"`
		Total    int64 `json:"totalTokenCount"`
	} `json:"usageMetadata"`
	ModelVersion string `json:"modelVersion"`
	ResponseID   string `json:"responseId"`
}

func (c chunk) usage() inference.TokenUsage {
	if c.Usage == nil {
		return inference.TokenUsage{}
	}
	return inference.TokenUsage{Input: c.Usage.Input, Output: c.Usage.Output + c.Usage.Thoughts, Total: c.Usage.Total, CachedInput: c.Usage.Cached, Reasoning: c.Usage.Thoughts}
}

func originData(sig string) json.RawMessage {
	if sig == "" {
		return nil
	}
	data, _ := json.Marshal(origin{ThoughtSignature: sig})
	return data
}

// appendPart adds one response part to the message and returns the event it makes in a
// stream: a text or reasoning delta, or a completed tool call.
func appendPart(m *inference.Message, part apiPart) (inference.GenerationEvent, error) {
	switch {
	case part.FunctionCall != nil:
		call := part.FunctionCall
		id := call.ID
		if id == "" {
			id = fmt.Sprintf("gemini-call-%d", len(m.ToolCalls))
		}
		args := call.Args
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		tc := inference.ToolCall{ID: id, Name: call.Name, Arguments: args, ProviderData: originData(part.ThoughtSignature)}
		m.ToolCalls = append(m.ToolCalls, tc)
		return inference.GenerationEvent{Kind: inference.EventKindToolCompleted, Index: len(m.ToolCalls) - 1, CallID: tc.ID, ToolName: tc.Name, Call: &tc}, nil
	case part.Text != nil:
		kind, event := inference.PartKindText, inference.EventKindTextDelta
		if part.Thought {
			kind, event = inference.PartKindReasoning, inference.EventKindReasoningDelta
		}
		n := len(m.Parts)
		if n > 0 && m.Parts[n-1].Kind == kind && len(m.Parts[n-1].ProviderData) == 0 {
			// A stream sends a part's text in pieces; they join into one part.
			m.Parts[n-1].Text += *part.Text
			m.Parts[n-1].ProviderData = originData(part.ThoughtSignature)
		} else {
			m.Parts = append(m.Parts, inference.Part{Kind: kind, Text: *part.Text, ProviderData: originData(part.ThoughtSignature)})
		}
		return inference.GenerationEvent{Kind: event, Index: len(m.Parts) - 1, Text: *part.Text}, nil
	case part.ThoughtSignature != "":
		// A signature on its own (as the last streamed piece can be) belongs to the part before.
		if n := len(m.Parts); n > 0 {
			m.Parts[n-1].ProviderData = originData(part.ThoughtSignature)
		}
		return inference.GenerationEvent{}, nil
	default:
		return inference.GenerationEvent{}, inference.ErrUnsupported
	}
}

func finishReason(reason string, toolCalls bool) (inference.FinishReason, error) {
	switch reason {
	case "STOP":
		if toolCalls {
			return inference.FinishReasonToolCalls, nil
		}
		return inference.FinishReasonStop, nil
	case "MAX_TOKENS":
		return inference.FinishReasonLength, nil
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
		return inference.FinishReasonContentFilter, nil
	default:
		return "", inference.ErrInvalidResponse
	}
}

func (p *provider) Generate(ctx context.Context, r inference.GenerationRequest) (inference.GenerationResponse, error) {
	body, err := p.payload(r)
	if err != nil {
		return inference.GenerationResponse{}, err
	}
	reply, err := p.send(ctx, http.MethodPost, "/models/"+r.Model+":generateContent", body)
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
	var result chunk
	if err := json.Unmarshal(raw, &result); err != nil || len(result.Candidates) == 0 {
		return inference.GenerationResponse{}, inference.ErrInvalidResponse
	}
	first := result.Candidates[0]
	if len(first.Content.Parts) == 0 {
		return inference.GenerationResponse{}, inference.ErrInvalidResponse
	}
	out := inference.GenerationResponse{ID: result.ResponseID, Model: r.Model, Message: inference.Message{Role: inference.RoleAssistant}, Usage: result.usage()}
	for _, part := range first.Content.Parts {
		if _, err := appendPart(&out.Message, part); err != nil {
			return inference.GenerationResponse{}, err
		}
	}
	if out.StopReason, err = finishReason(first.FinishReason, len(out.Message.ToolCalls) > 0); err != nil {
		return inference.GenerationResponse{}, err
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
