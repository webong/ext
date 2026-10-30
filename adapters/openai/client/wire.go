// Package openai implements the inference.provider/v1 contract for the
// OpenAI API. wire.go handles OpenAI-specific request/response encoding.
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/webong/ext/res/int/inference"
)

// chatMessage maps an inference.Message to an OpenAI chat message.
type chatMessage struct {
	Role      string             `json:"role"`
	Content   json.RawMessage    `json:"content,omitempty"`
	ToolCalls []chatToolCallResp `json:"tool_calls,omitempty"`
}

// chatToolCallResp maps an OpenAI tool call response.
type chatToolCallResp struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatToolFunction `json:"function"`
}

// chatToolFunction maps a tool definition/call to OpenAI format.
type chatToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// chatToolDef is an OpenAI tool definition in a request.
type chatToolDef struct {
	Type     string           `json:"type"`
	Function chatToolFunction `json:"function"`
}

// chatRequest is the OpenAI chat completions request.
type chatRequest struct {
	Model       string          `json:"model"`
	Messages    []chatMessage   `json:"messages"`
	Tools       []chatToolDef   `json:"tools,omitempty"`
	ToolChoice  json.RawMessage `json:"tool_choice,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

// chatResponse is the OpenAI chat completions response.
type chatResponse struct {
	ID      string   `json:"id"`
	Model   string   `json:"model"`
	Choices []choice `json:"choices"`
	Usage   usage    `json:"usage"`
}

// choice is one OpenAI completion choice.
type choice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// usage is OpenAI token usage.
type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// embedRequest is the OpenAI embeddings request.
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embedResponse is the OpenAI embeddings response.
type embedResponse struct {
	Object string       `json:"object"`
	Model  string       `json:"model"`
	Data   []embedDatum `json:"data"`
	Usage  usage        `json:"usage"`
}

// embedDatum is one embedding result.
type embedDatum struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

// modelListResponse is the OpenAI models list response.
type modelListResponse struct {
	Object string       `json:"object"`
	Data   []modelDatum `json:"data"`
}

// modelDatum is one model entry.
type modelDatum struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

// openAIError is the OpenAI error object.
type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// openAIErrorResponse is an OpenAI error response envelope.
type openAIErrorResponse struct {
	Error openAIError `json:"error"`
}

// marshalChatRequest converts an inference.GenerationRequest to an OpenAI
// chat completions request body.
func marshalChatRequest(req inference.GenerationRequest, stream bool) ([]byte, error) {
	if len(req.OutputSchema) != 0 {
		return nil, inference.ErrUnsupported
	}
	messages := make([]chatMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		content, err := marshalContent(m.Parts)
		if err != nil {
			return nil, err
		}
		msg := chatMessage{Role: string(m.Role), Content: content}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, chatToolCallResp{
				ID:   tc.ID,
				Type: "function",
				Function: chatToolFunction{
					Name:       tc.Name,
					Parameters: tc.Arguments,
				},
			})
		}
		messages = append(messages, msg)
	}

	tools := make([]chatToolDef, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, chatToolDef{
			Type: "function",
			Function: chatToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	var toolChoice json.RawMessage
	if len(req.Tools) > 0 {
		toolChoice = json.RawMessage(`"auto"`)
	}

	cr := chatRequest{
		Model:      req.Model,
		Messages:   messages,
		Tools:      tools,
		ToolChoice: toolChoice,
		MaxTokens:  req.MaxOutputTokens,
		Stream:     stream,
	}
	if req.Temperature != nil {
		cr.Temperature = req.Temperature
	}
	return json.Marshal(cr)
}

// marshalContent converts inference parts to OpenAI content.
func marshalContent(parts []inference.Part) (json.RawMessage, error) {
	textParts := make([]string, 0)
	for _, part := range parts {
		if part.Kind != inference.PartKindText {
			return nil, inference.ErrUnsupported
		}
		textParts = append(textParts, part.Text)
	}
	if len(textParts) == 0 {
		return json.RawMessage(`""`), nil
	}
	return json.Marshal(strings.Join(textParts, "\n"))
}

// parseChatResponse converts an OpenAI chat response to inference.GenerationResponse.
func parseChatResponse(raw []byte) (inference.GenerationResponse, error) {
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return inference.GenerationResponse{}, inference.NewRemoteError(
			inference.ErrInvalidResponse, "failed to decode response", "openai")
	}
	if len(cr.Choices) == 0 {
		return inference.GenerationResponse{}, inference.NewRemoteError(
			inference.ErrInvalidResponse, "no choices in response", "openai")
	}
	choice := cr.Choices[0]
	resp := inference.GenerationResponse{
		ID:      cr.ID,
		Model:   cr.Model,
		Message: inference.Message{Role: inference.RoleAssistant},
		Usage: inference.TokenUsage{
			Input:  int64(cr.Usage.PromptTokens),
			Output: int64(cr.Usage.CompletionTokens),
			Total:  int64(cr.Usage.TotalTokens),
		},
		StopReason: finishReason(choice.FinishReason),
	}
	if choice.Message.Content != nil {
		var text string
		if err := json.Unmarshal(choice.Message.Content, &text); err == nil {
			resp.Message.Parts = []inference.Part{{
				Kind: inference.PartKindText,
				Text: text,
			}}
		}
	}
	for _, tc := range choice.Message.ToolCalls {
		resp.Message.ToolCalls = append(resp.Message.ToolCalls, inference.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Parameters,
		})
	}
	return resp, nil
}

// marshalEmbedRequest converts an inference.EmbeddingRequest to an OpenAI
// embeddings request body.
func marshalEmbedRequest(req inference.EmbeddingRequest) ([]byte, error) {
	er := embedRequest{
		Model: req.Model,
		Input: req.Inputs,
	}
	return json.Marshal(er)
}

// parseEmbedResponse converts an OpenAI embeddings response to inference.EmbeddingResponse.
func parseEmbedResponse(raw []byte) (inference.EmbeddingResponse, error) {
	var er embedResponse
	if err := json.Unmarshal(raw, &er); err != nil {
		return inference.EmbeddingResponse{}, inference.NewRemoteError(
			inference.ErrInvalidResponse, "failed to decode embedding response", "openai")
	}
	embeddings := make([]inference.Embedding, 0, len(er.Data))
	for _, d := range er.Data {
		embeddings = append(embeddings, inference.Embedding{
			Index:  d.Index,
			Vector: d.Embedding,
		})
	}
	return inference.EmbeddingResponse{
		Model:      er.Model,
		Embeddings: embeddings,
		Usage: inference.TokenUsage{
			Input: int64(er.Usage.PromptTokens),
			Total: int64(er.Usage.TotalTokens),
		},
	}, nil
}

// parseModelList converts an OpenAI models list response to inference.ModelInfo.
func parseModelList(raw []byte) ([]inference.ModelInfo, error) {
	var ml modelListResponse
	if err := json.Unmarshal(raw, &ml); err != nil {
		return nil, inference.NewRemoteError(
			inference.ErrInvalidResponse, "failed to decode model list", "openai")
	}
	models := make([]inference.ModelInfo, 0, len(ml.Data))
	for _, d := range ml.Data {
		models = append(models, inference.ModelInfo{
			ID:       d.ID,
			Name:     d.ID,
			Provider: "openai",
		})
	}
	return models, nil
}

// finishReason maps an OpenAI finish reason to inference.FinishReason.
func finishReason(s string) inference.FinishReason {
	switch s {
	case "stop":
		return inference.FinishReasonStop
	case "length":
		return inference.FinishReasonLength
	case "tool_calls":
		return inference.FinishReasonToolCalls
	case "content_filter":
		return inference.FinishReasonContentFilter
	default:
		return inference.FinishReasonStop
	}
}

// mapOpenAIError maps an OpenAI error response to an inference.RemoteError.
func mapOpenAIError(raw []byte, statusCode int) error {
	var oerr openAIErrorResponse
	if err := json.Unmarshal(raw, &oerr); err != nil {
		return inference.NewRemoteError(mapStatusCode(statusCode),
			"provider error", "openai")
	}
	code := inference.ErrInvalidResponse
	switch oerr.Error.Type {
	case "invalid_request_error":
		code = inference.ErrInvalidRequest
	case "authentication_error":
		code = inference.ErrAuthentication
	case "permission_error":
		code = inference.ErrAuthorization
	case "rate_limit_error":
		code = inference.ErrRateLimit
	case "not_found_error":
		code = inference.ErrUnavailable
	}
	return inference.NewRemoteError(code, code.Error(), "openai")
}

// mapStatusCode maps an HTTP status code to an inference error category.
func mapStatusCode(statusCode int) error {
	switch {
	case statusCode == 401:
		return inference.ErrAuthentication
	case statusCode == 403:
		return inference.ErrAuthorization
	case statusCode == 429:
		return inference.ErrRateLimit
	case statusCode >= 400 && statusCode < 500:
		return inference.ErrInvalidRequest
	case statusCode >= 500:
		return inference.ErrUnavailable
	default:
		return inference.ErrInvalidResponse
	}
}

// chatChunk is one OpenAI streaming chunk.
type chatChunk struct {
	ID      string        `json:"id"`
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
}

// chunkChoice is one choice in a streaming chunk.
type chunkChoice struct {
	Index        int         `json:"index"`
	Delta        chatMessage `json:"delta"`
	FinishReason string      `json:"finish_reason"`
}

// streamEvents streams generation events from OpenAI's SSE stream.
func (p *Provider) streamEvents(ctx context.Context, path string, body []byte, sink inference.EventSink) (inference.GenerationResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+path, strings.NewReader(string(body)))
	if err != nil {
		return inference.GenerationResponse{}, err
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return inference.GenerationResponse{}, inference.NewRemoteError(
				inference.ErrCanceled, "operation canceled", "openai")
		}
		return inference.GenerationResponse{}, inference.NewRemoteError(
			inference.ErrUnavailable, "request failed", "openai")
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(io.LimitReader(resp.Body, (32<<20)+1))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		return inference.GenerationResponse{}, mapOpenAIError(raw, resp.StatusCode)
	}

	var final inference.GenerationResponse
	completed := false
	final.Message.Role = inference.RoleAssistant

	for {
		if err := ctx.Err(); err != nil {
			return final, inference.NewRemoteError(inference.ErrCanceled, "operation canceled", "openai")
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return final, inference.NewRemoteError(inference.ErrInvalidResponse, "stream read failed", "openai")
		}
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			completed = true
			break
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return final, inference.NewRemoteError(inference.ErrInvalidResponse, "malformed stream event", "openai")
		}
		if chunk.ID != "" && final.ID == "" {
			final.ID = chunk.ID
		}
		if chunk.Model != "" && final.Model == "" {
			final.Model = chunk.Model
		}
		if len(chunk.Choices) > 0 {
			cc := chunk.Choices[0]
			if cc.Delta.Content != nil {
				var text string
				if err := json.Unmarshal(cc.Delta.Content, &text); err == nil && text != "" {
					ev := inference.GenerationEvent{
						Kind:  inference.EventKindTextDelta,
						Text:  text,
						Index: cc.Index,
					}
					if err := sink.Emit(ctx, ev); err != nil {
						return final, inference.NewRemoteError(inference.ErrCanceled, "stream canceled by sink", "openai")
					}
				}
			}
			if cc.FinishReason != "" {
				final.StopReason = finishReason(cc.FinishReason)
			}
		}
	}

	if !completed {
		return final, inference.NewRemoteError(inference.ErrInvalidResponse, "stream ended before completion", "openai")
	}
	return final, nil
}
