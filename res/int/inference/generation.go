// Package inference generation types define multimodal generation requests and
// responses. Parts carry text, reasoning, refusal, image, audio, video, file,
// or opaque provider context. The neutral layer never interprets part payloads
// beyond bounds and shape validation.
package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"strings"
)

// Role is the sender of a message.
type Role string

const (
	// RoleSystem is a system instruction.
	RoleSystem Role = "system"
	// RoleUser is a user message.
	RoleUser Role = "user"
	// RoleAssistant is an assistant message.
	RoleAssistant Role = "assistant"
	// RoleTool is a tool-result message.
	RoleTool Role = "tool"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	switch r {
	case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		return true
	default:
		return false
	}
}

// PartKind is one kind of message content.
type PartKind string

const (
	// PartKindText is ordinary text content.
	PartKindText PartKind = "text"
	// PartKindReasoning is opaque model reasoning, returned only to its origin.
	PartKindReasoning PartKind = "reasoning"
	// PartKindRefusal is a refusal to comply.
	PartKindRefusal PartKind = "refusal"
	// PartKindImage is an image part.
	PartKindImage PartKind = "image"
	// PartKindAudio is an audio part.
	PartKindAudio PartKind = "audio"
	// PartKindVideo is a video part.
	PartKindVideo PartKind = "video"
	// PartKindFile is a file part or approved reference.
	PartKindFile PartKind = "file"
	// PartKindContext is opaque provider context echoed back to its origin.
	PartKindContext PartKind = "context"
)

// Valid reports whether k is a known part kind.
func (k PartKind) Valid() bool {
	switch k {
	case PartKindText, PartKindReasoning, PartKindRefusal, PartKindImage,
		PartKindAudio, PartKindVideo, PartKindFile, PartKindContext:
		return true
	default:
		return false
	}
}

// MediaReference is passed to the selected adapter. Libraries never download
// URLs or upload local files implicitly. Provider file IDs are opaque.
type MediaReference struct {
	// Kind is "url" or "file".
	Kind string `json:"kind"`
	// URI is the reference. For "url" it must use HTTPS with no user or fragment.
	URI string `json:"uri"`
}

// Validate checks a media reference.
func (r MediaReference) Validate() error {
	switch r.Kind {
	case "url":
		u, err := url.Parse(r.URI)
		if err != nil || u.Host == "" || u.Scheme != "https" || u.User != nil || u.Fragment != "" {
			return fmt.Errorf("%w: media URL must use HTTPS without user or fragment", ErrInvalidRequest)
		}
	case "file":
		if r.URI == "" || len(r.URI) > 8192 {
			return fmt.Errorf("%w: media file reference is required and must be at most 8192 bytes", ErrInvalidRequest)
		}
	default:
		return fmt.Errorf("%w: media reference kind must be url or file", ErrInvalidRequest)
	}
	return nil
}

// Part is one item of multimodal content. Exactly one of Data or Reference is
// set for binary parts; ProviderData is opaque context echoed only to its
// originating provider/model.
type Part struct {
	// Kind is the part kind.
	Kind PartKind `json:"kind"`
	// Text is the text content for text/reasoning/refusal parts.
	Text string `json:"text,omitempty"`
	// MediaType is the MIME type for binary parts.
	MediaType string `json:"mediaType,omitempty"`
	// Data is the inline binary content. Binary parts use either Data or Reference.
	Data []byte `json:"data,omitempty"`
	// Reference is an approved file or URL reference.
	Reference *MediaReference `json:"reference,omitempty"`
	// ProviderData is opaque provider context, passed back only to its origin.
	ProviderData json.RawMessage `json:"providerData,omitempty"`
}

// Validate checks a part's shape and bounds. Provider adapters enforce their
// own modality and option restrictions.
func (p Part) Validate(role Role) error {
	if !p.Kind.Valid() {
		return fmt.Errorf("%w: unknown part kind %q", ErrInvalidRequest, p.Kind)
	}
	if len(p.ProviderData) > 0 && !json.Valid(p.ProviderData) {
		return fmt.Errorf("%w: provider context must be valid JSON", ErrInvalidRequest)
	}
	switch p.Kind {
	case PartKindContext:
		if role != RoleAssistant || len(p.ProviderData) == 0 || p.Text != "" || p.MediaType != "" ||
			len(p.Data) != 0 || p.Reference != nil {
			return fmt.Errorf("%w: opaque model context is assistant-only with providerData", ErrInvalidRequest)
		}
	case PartKindText, PartKindReasoning, PartKindRefusal:
		if p.MediaType != "" || len(p.Data) != 0 || p.Reference != nil {
			return fmt.Errorf("%w: text/reasoning/refusal parts must not carry media", ErrInvalidRequest)
		}
		if p.Kind != PartKindText && role != RoleAssistant {
			return fmt.Errorf("%w: %s parts are assistant-only", ErrInvalidRequest, p.Kind)
		}
	case PartKindImage, PartKindAudio, PartKindVideo, PartKindFile:
		if _, _, err := mime.ParseMediaType(p.MediaType); err != nil || !strings.Contains(p.MediaType, "/") {
			return fmt.Errorf("%w: binary part needs a valid media type", ErrInvalidRequest)
		}
		if p.Text != "" {
			return fmt.Errorf("%w: binary part must not carry text", ErrInvalidRequest)
		}
		if (len(p.Data) == 0) == (p.Reference == nil) {
			return fmt.Errorf("%w: binary part needs exactly one of data or reference", ErrInvalidRequest)
		}
		if p.Reference != nil {
			if err := p.Reference.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

// ToolDefinition is one tool the caller offers. InputSchema is a JSON Schema
// object describing accepted arguments.
type ToolDefinition struct {
	// Name is the tool name.
	Name string `json:"name"`
	// Description documents the tool.
	Description string `json:"description,omitempty"`
	// InputSchema is a JSON Schema object for the tool arguments.
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Validate checks a tool definition.
func (t ToolDefinition) Validate() error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("%w: tool name is required", ErrInvalidRequest)
	}
	if !jsonObject(t.InputSchema) {
		return fmt.Errorf("%w: tool inputSchema must be a JSON object", ErrInvalidRequest)
	}
	return nil
}

// ToolCall is one proposed tool call. The host never executes tools
// automatically; it presents the proposal to the caller.
type ToolCall struct {
	// ID is the unique call identifier.
	ID string `json:"id"`
	// Name is the tool name.
	Name string `json:"name"`
	// Arguments are the tool arguments as a JSON object.
	Arguments json.RawMessage `json:"arguments"`
	// ProviderData is opaque provider context.
	ProviderData json.RawMessage `json:"providerData,omitempty"`
}

// Validate checks a tool call.
func (t ToolCall) Validate() error {
	if t.ID == "" || strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("%w: tool call id and name are required", ErrInvalidRequest)
	}
	if !jsonObject(t.Arguments) {
		return fmt.Errorf("%w: tool call arguments must be a JSON object", ErrInvalidRequest)
	}
	if len(t.ProviderData) > 0 && !json.Valid(t.ProviderData) {
		return fmt.Errorf("%w: tool call providerData must be valid JSON", ErrInvalidRequest)
	}
	return nil
}

// Message is one multimodal message in a conversation.
type Message struct {
	// Role is the sender.
	Role Role `json:"role"`
	// Parts are the message content.
	Parts []Part `json:"parts,omitempty"`
	// ToolCalls are proposed calls (assistant messages only).
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
	// ToolCallID links a tool-result message to its call.
	ToolCallID string `json:"toolCallId,omitempty"`
	// ToolName names the tool producing the result.
	ToolName string `json:"toolName,omitempty"`
	// ToolError marks a tool result as failed.
	ToolError bool `json:"toolError,omitempty"`
}

// Validate checks a message.
func (m Message) Validate() error {
	if !m.Role.Valid() {
		return fmt.Errorf("%w: unknown message role %q", ErrInvalidRequest, m.Role)
	}
	if m.Role == RoleTool && m.ToolCallID == "" {
		return fmt.Errorf("%w: tool result needs a call ID", ErrInvalidRequest)
	}
	if m.ToolError && m.Role != RoleTool {
		return fmt.Errorf("%w: failure flag belongs to a tool result", ErrInvalidRequest)
	}
	if len(m.ToolCalls) > 0 && m.Role != RoleAssistant {
		return fmt.Errorf("%w: only assistant messages contain tool calls", ErrInvalidRequest)
	}
	if len(m.Parts) == 0 && len(m.ToolCalls) == 0 {
		return fmt.Errorf("%w: empty message", ErrInvalidRequest)
	}
	seen := map[string]bool{}
	for _, part := range m.Parts {
		if err := part.Validate(m.Role); err != nil {
			return err
		}
	}
	for _, call := range m.ToolCalls {
		if call.ID == "" || seen[call.ID] {
			return fmt.Errorf("%w: duplicate or empty tool call id", ErrInvalidRequest)
		}
		if err := call.Validate(); err != nil {
			return err
		}
		seen[call.ID] = true
	}
	return nil
}

// GenerationRequest is a request to generate content.
type GenerationRequest struct {
	// Model is the selected model identity.
	Model string `json:"model"`
	// Messages are the conversation.
	Messages []Message `json:"messages"`
	// Tools are offered tool definitions.
	Tools []ToolDefinition `json:"tools,omitempty"`
	// MaxOutputTokens bounds generated output tokens.
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
	// Temperature is the sampling temperature, where supported.
	Temperature *float64 `json:"temperature,omitempty"`
	// OutputSchema is a JSON Schema for structured object output.
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	// Options are provider-owned options with a restricted vocabulary.
	Options json.RawMessage `json:"options,omitempty"`
	// Limits are explicit bounds for this request.
	Limits Limits `json:"limits,omitempty"`
}

// Validate checks the neutral contract. Provider adapters enforce their own
// modality, option, and model restrictions explicitly.
func (r GenerationRequest) Validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("%w: model is required", ErrInvalidRequest)
	}
	if len(r.Messages) == 0 || len(r.Messages) > 4096 {
		return fmt.Errorf("%w: messages must be between 1 and 4096", ErrInvalidRequest)
	}
	if err := r.Limits.Validate(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, tool := range r.Tools {
		if strings.TrimSpace(tool.Name) == "" || seen[tool.Name] {
			return fmt.Errorf("%w: duplicate or empty tool name", ErrInvalidRequest)
		}
		if err := tool.Validate(); err != nil {
			return err
		}
		seen[tool.Name] = true
	}
	if len(r.OutputSchema) != 0 && !jsonObject(r.OutputSchema) {
		return fmt.Errorf("%w: output schema must be a JSON object", ErrInvalidRequest)
	}
	if len(r.Options) != 0 && !jsonObject(r.Options) {
		return fmt.Errorf("%w: adapter options must be a JSON object", ErrInvalidRequest)
	}
	for _, message := range r.Messages {
		if err := message.Validate(); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(r)
	if err != nil || len(encoded) > 32<<20 {
		return fmt.Errorf("%w: request exceeds 32 MiB or cannot be encoded", ErrInvalidRequest)
	}
	return nil
}

// FinishReason is why generation ended.
type FinishReason string

const (
	// FinishReasonStop means the model reached a natural stopping point.
	FinishReasonStop FinishReason = "stop"
	// FinishReasonLength means output hit a token or byte limit.
	FinishReasonLength FinishReason = "length"
	// FinishReasonToolCalls means the model proposed tool calls.
	FinishReasonToolCalls FinishReason = "toolCalls"
	// FinishReasonContentFilter means output was filtered.
	FinishReasonContentFilter FinishReason = "contentFilter"
	// FinishReasonRefusal means the model refused.
	FinishReasonRefusal FinishReason = "refusal"
)

// Valid reports whether f is a known finish reason.
func (f FinishReason) Valid() bool {
	switch f {
	case FinishReasonStop, FinishReasonLength, FinishReasonToolCalls,
		FinishReasonContentFilter, FinishReasonRefusal:
		return true
	default:
		return false
	}
}

// TokenUsage reports token accounting.
type TokenUsage struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	Total       int64 `json:"total"`
	CachedInput int64 `json:"cachedInput,omitempty"`
	Reasoning   int64 `json:"reasoning,omitempty"`
}

// GenerationResponse is the result of a generation call.
type GenerationResponse struct {
	// ID is the provider-assigned response ID.
	ID string `json:"id,omitempty"`
	// Model is the model that served the request.
	Model string `json:"model"`
	// Message is the assistant message.
	Message Message `json:"message"`
	// StopReason is why generation ended.
	StopReason FinishReason `json:"stopReason,omitempty"`
	// Usage is token accounting.
	Usage TokenUsage `json:"usage"`
	// ProviderMetadata is opaque provider metadata, free of credentials.
	ProviderMetadata json.RawMessage `json:"providerMetadata,omitempty"`
	// ReasoningContext is opaque reasoning context. It may only be returned to
	// its originating provider/model and must never be echoed elsewhere.
	ReasoningContext json.RawMessage `json:"reasoningContext,omitempty"`
}

// Text returns only text parts.
func (r GenerationResponse) Text() string {
	var b strings.Builder
	for _, part := range r.Message.Parts {
		if part.Kind == PartKindText {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

// HasToolCalls reports whether the response proposes tool calls.
func (r GenerationResponse) HasToolCalls() bool {
	return len(r.Message.ToolCalls) > 0
}

// IsRefusal reports whether the response is a refusal.
func (r GenerationResponse) IsRefusal() bool {
	return r.StopReason == FinishReasonRefusal || r.Message.PartsHasKind(PartKindRefusal)
}

// PartsHasKind reports whether the message has a part of the given kind.
func (m Message) PartsHasKind(kind PartKind) bool {
	for _, p := range m.Parts {
		if p.Kind == kind {
			return true
		}
	}
	return false
}

// Validate checks a response shape.
func (r GenerationResponse) Validate() error {
	if r.Model == "" || r.Message.Role != RoleAssistant {
		return fmt.Errorf("%w: response needs a model and assistant message", ErrInvalidResponse)
	}
	if err := r.Message.Validate(); err != nil {
		return err
	}
	if r.StopReason != "" && !r.StopReason.Valid() {
		return fmt.Errorf("%w: unknown finish reason %q", ErrInvalidResponse, r.StopReason)
	}
	if r.Usage.Input < 0 || r.Usage.Output < 0 || r.Usage.Total < 0 ||
		r.Usage.CachedInput < 0 || r.Usage.Reasoning < 0 {
		return fmt.Errorf("%w: usage must not be negative", ErrInvalidResponse)
	}
	if len(r.ReasoningContext) > 0 && !json.Valid(r.ReasoningContext) {
		return fmt.Errorf("%w: reasoning context must be valid JSON", ErrInvalidResponse)
	}
	return nil
}

// GenerationEvent is one streaming event.
type GenerationEvent struct {
	// Kind is the event kind.
	Kind string `json:"kind"`
	// Text is a text delta.
	Text string `json:"text,omitempty"`
	// Index is the part index for deltas.
	Index int `json:"index,omitempty"`
	// CallID links tool deltas to a call.
	CallID string `json:"callId,omitempty"`
	// ToolName names the tool being proposed.
	ToolName string `json:"toolName,omitempty"`
	// ArgumentsDelta is a partial tool-argument delta.
	ArgumentsDelta string `json:"argumentsDelta,omitempty"`
	// Response is the final response on completed events.
	Response *GenerationResponse `json:"response,omitempty"`
	// Call is a completed tool-call proposal.
	Call *ToolCall `json:"call,omitempty"`
	// Usage is incremental token usage.
	Usage *TokenUsage `json:"usage,omitempty"`
}

// EventSink receives streaming events. An emit error stops the request and
// provides backpressure and cancellation without a background loop.
type EventSink interface {
	// Emit receives one event. Returning an error terminates the stream.
	Emit(ctx context.Context, event GenerationEvent) error
}

// SinkFunc adapts a function to EventSink.
type SinkFunc func(context.Context, GenerationEvent) error

func (f SinkFunc) Emit(ctx context.Context, event GenerationEvent) error { return f(ctx, event) }

// StreamingEventKinds are the recognized streaming event kinds.
const (
	EventKindStarted       = "started"
	EventKindTextDelta     = "text.delta"
	EventKindReasoningDelta = "reasoning.delta"
	EventKindRefusalDelta   = "refusal.delta"
	EventKindToolDelta      = "tool.delta"
	EventKindToolCompleted  = "tool.completed"
	EventKindUsage          = "usage"
	EventKindCompleted      = "completed"
	EventKindError          = "error"
)

func jsonObject(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && value != nil
}

var _ = errors.Unwrap
