// Package realtime defines explicit duplex model sessions. It owns no audio
// device, tool executor, transcript store, or endpoint selection.
package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/webong/ext/res/int/inference"
)

const APIVersion = "intelligence.realtime/v1"

// OpenRequest starts a caller-selected model session. Options are interpreted
// only by the selected provider; no endpoint or credential is implicit.
type OpenRequest struct {
	APIVersion string          `json:"apiVersion"`
	Model      string          `json:"model"`
	Options    json.RawMessage `json:"options,omitempty"`
}

func (r OpenRequest) Validate() error {
	if r.APIVersion != APIVersion || strings.TrimSpace(r.Model) == "" || len(r.Model) > 256 || len(r.Options) > 1<<20 || len(r.Options) > 0 && !json.Valid(r.Options) {
		return fmt.Errorf("%w: invalid realtime open request", inference.ErrInvalidRequest)
	}
	if len(r.Options) > 0 {
		var object map[string]json.RawMessage
		if json.Unmarshal(r.Options, &object) != nil || object == nil {
			return fmt.Errorf("%w: realtime options must be a JSON object", inference.ErrInvalidRequest)
		}
	}
	return nil
}

// Provider opens a model session. A host grants access before opening it.
type Provider interface {
	Open(context.Context, OpenRequest) (Session, error)
	Close() error
}

// Session sends caller-selected turns and receives provider events. A tool
// proposal is data; it is never executed by this interface.
type Session interface {
	SendText(context.Context, string) error
	SendAudio(context.Context, []byte) error
	CommitAudio(context.Context) error
	Respond(context.Context) error
	Interrupt(context.Context) error
	SendToolResult(context.Context, string, []byte) error
	Receive(context.Context) (Event, error)
	Close() error
}

// AudioControl is optional. Canceling output and truncating conversation
// audio to what was played are distinct caller decisions.
type AudioControl interface {
	ClearInputAudio(context.Context) error
	TruncateAudio(context.Context, string, int, int) error
}

type Event struct {
	Kind       string                `json:"kind"`
	NativeType string                `json:"nativeType,omitempty"`
	ID         string                `json:"id,omitempty"`
	ItemID     string                `json:"itemId,omitempty"`
	ResponseID string                `json:"responseId,omitempty"`
	Status     string                `json:"status,omitempty"`
	Text       string                `json:"text,omitempty"`
	Audio      []byte                `json:"audio,omitempty"`
	Call       *inference.ToolCall   `json:"call,omitempty"`
	Usage      *inference.TokenUsage `json:"usage,omitempty"`
	OccurredAt time.Time             `json:"occurredAt"`
	Raw        []byte                `json:"raw,omitempty"`
}

// Validate rejects unbounded event data before it crosses a plugin boundary.
func (e Event) Validate() error {
	if e.Kind == "" || len(e.Kind) > 128 || len(e.NativeType) > 256 || len(e.Text) > 4<<20 || len(e.Audio) > 4<<20 || len(e.Raw) > 16<<20 || len(e.ID) > 256 || len(e.ItemID) > 256 || len(e.ResponseID) > 256 || len(e.Status) > 128 {
		return fmt.Errorf("%w: invalid realtime event", inference.ErrInvalidResponse)
	}
	if e.Usage != nil && (e.Usage.Input < 0 || e.Usage.Output < 0 || e.Usage.Total < 0) {
		return fmt.Errorf("%w: negative usage", inference.ErrInvalidResponse)
	}
	return nil
}

// State is a pure reducer. It never starts a session or performs an action.
type State struct {
	Connection     string    `json:"connection"`
	ResponseID     string    `json:"responseId,omitempty"`
	ResponseStatus string    `json:"responseStatus,omitempty"`
	LastEventAt    time.Time `json:"lastEventAt"`
}

func (s State) Apply(event Event) State {
	s.LastEventAt = event.OccurredAt
	switch event.Kind {
	case "session.ready":
		s.Connection = "ready"
	case "session.closed":
		s.Connection = "closed"
	case "error":
		s.Connection = "error"
	case "response.started":
		s.ResponseID = event.ResponseID
		s.ResponseStatus = "responding"
	case "response.completed", "response.interrupted":
		s.ResponseID = event.ResponseID
		s.ResponseStatus = event.Status
	}
	return s
}
