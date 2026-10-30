// Package realtime binds intelligence.realtime/v1 to the Ext plugin engine.
// It transports explicit model-session operations and never opens devices or
// executes proposed tools.
package realtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/author"
	domain "github.com/webong/ext/res/int/realtime"
)

const ContractName = "intelligence.realtime"
const ContractVersion = "v1"
const Protocol = ContractName + "/" + ContractVersion

var ContractRef = plugin.ContractRef{Name: ContractName, Version: ContractVersion}

type sessionID struct {
	ID string `json:"id"`
}
type textInput struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}
type audioInput struct {
	ID    string `json:"id"`
	Audio []byte `json:"audio"`
}
type toolInput struct {
	ID     string `json:"id"`
	CallID string `json:"callId"`
	Result []byte `json:"result"`
}
type truncationInput struct {
	ID           string `json:"id"`
	ItemID       string `json:"itemId"`
	ContentIndex int    `json:"contentIndex"`
	PlayedMS     int    `json:"playedMs"`
}

func method[I, O any](name string, validateInput func(I) error, validateOutput func(O) error) author.Method[I, O] {
	return author.Method[I, O]{Contract: ContractRef, Operation: plugin.Operation{Name: name, Surface: "action"}, ValidateInput: validateInput, ValidateOutput: validateOutput}
}
func openMethod() author.Method[domain.OpenRequest, sessionID] {
	return method[domain.OpenRequest, sessionID]("open", domain.OpenRequest.Validate, nil)
}
func idMethod(name string) author.Method[sessionID, struct{}] {
	return method[sessionID, struct{}](name, validateID, nil)
}
func receiveMethod() author.Method[sessionID, domain.Event] {
	return method[sessionID, domain.Event]("receive", validateID, domain.Event.Validate)
}
func textMethod() author.Method[textInput, struct{}] {
	return method[textInput, struct{}]("send_text", func(v textInput) error {
		if err := validateID(sessionID{v.ID}); err != nil {
			return err
		}
		if v.Text == "" || len(v.Text) > 4<<20 {
			return plugin.ErrInvalid
		}
		return nil
	}, nil)
}
func audioMethod() author.Method[audioInput, struct{}] {
	return method[audioInput, struct{}]("send_audio", func(v audioInput) error {
		if err := validateID(sessionID{v.ID}); err != nil {
			return err
		}
		if len(v.Audio) == 0 || len(v.Audio) > 4<<20 {
			return plugin.ErrInvalid
		}
		return nil
	}, nil)
}
func toolMethod() author.Method[toolInput, struct{}] {
	return method[toolInput, struct{}]("send_tool_result", func(v toolInput) error {
		if err := validateID(sessionID{v.ID}); err != nil {
			return err
		}
		if v.CallID == "" || len(v.CallID) > 256 || len(v.Result) > 4<<20 {
			return plugin.ErrInvalid
		}
		return nil
	}, nil)
}
func truncationMethod() author.Method[truncationInput, struct{}] {
	return method[truncationInput, struct{}]("truncate_audio", func(v truncationInput) error {
		if err := validateID(sessionID{v.ID}); err != nil {
			return err
		}
		if v.ItemID == "" || len(v.ItemID) > 256 || v.ContentIndex < 0 || v.PlayedMS < 0 {
			return plugin.ErrInvalid
		}
		return nil
	}, nil)
}
func validateID(v sessionID) error {
	if len(v.ID) != 32 {
		return plugin.ErrInvalid
	}
	_, err := hex.DecodeString(v.ID)
	if err != nil {
		return fmt.Errorf("%w: invalid session ID", plugin.ErrInvalid)
	}
	return nil
}

func Descriptor(identity plugin.Identity) plugin.Descriptor {
	operations := []plugin.Operation{}
	for _, name := range []string{"open", "send_text", "send_audio", "commit_audio", "respond", "interrupt", "send_tool_result", "receive", "clear_input_audio", "truncate_audio", "close"} {
		operations = append(operations, plugin.Operation{Name: name, Surface: "action"})
	}
	return plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: identity, Contracts: []plugin.Contract{{ContractRef: ContractRef, Operations: operations}}}
}

// Guest keeps one live model session per provider process. Hosts that need
// parallel conversations launch separate verified processes.
type Guest struct {
	provider domain.Provider
	mu       sync.Mutex
	id       string
	session  domain.Session
}

func RegisterProvider(registry *author.Registry, provider domain.Provider, authorize func(context.Context, plugin.Request) error) (*plugin.Descriptor, error) {
	descriptor, _, err := registerProvider(registry, provider, authorize)
	return descriptor, err
}

func registerProvider(registry *author.Registry, provider domain.Provider, authorize func(context.Context, plugin.Request) error) (*plugin.Descriptor, *Guest, error) {
	if registry == nil || provider == nil || authorize == nil {
		return nil, nil, plugin.ErrInvalid
	}
	g := &Guest{provider: provider}
	if err := author.Register(registry, openMethod(), func(ctx context.Context, _ plugin.Request, request domain.OpenRequest) (sessionID, error) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.session != nil {
			return sessionID{}, fmt.Errorf("%w: session already open", plugin.ErrDenied)
		}
		session, err := provider.Open(ctx, request)
		if err != nil {
			return sessionID{}, err
		}
		if session == nil {
			return sessionID{}, plugin.ErrInvalid
		}
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			_ = session.Close()
			return sessionID{}, err
		}
		g.id, g.session = hex.EncodeToString(token[:]), session
		return sessionID{g.id}, nil
	}); err != nil {
		return nil, nil, err
	}
	with := func(id string) (domain.Session, error) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.session == nil || id != g.id {
			return nil, plugin.ErrDenied
		}
		return g.session, nil
	}
	if err := author.Register(registry, textMethod(), func(ctx context.Context, _ plugin.Request, v textInput) (struct{}, error) {
		s, err := with(v.ID)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, s.SendText(ctx, v.Text)
	}); err != nil {
		return nil, nil, err
	}
	if err := author.Register(registry, audioMethod(), func(ctx context.Context, _ plugin.Request, v audioInput) (struct{}, error) {
		s, err := with(v.ID)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, s.SendAudio(ctx, v.Audio)
	}); err != nil {
		return nil, nil, err
	}
	for _, operation := range []struct {
		name string
		call func(context.Context, domain.Session) error
	}{
		{"commit_audio", func(ctx context.Context, s domain.Session) error { return s.CommitAudio(ctx) }},
		{"respond", func(ctx context.Context, s domain.Session) error { return s.Respond(ctx) }},
		{"interrupt", func(ctx context.Context, s domain.Session) error { return s.Interrupt(ctx) }},
	} {
		operation := operation
		if err := author.Register(registry, idMethod(operation.name), func(ctx context.Context, _ plugin.Request, v sessionID) (struct{}, error) {
			s, err := with(v.ID)
			if err != nil {
				return struct{}{}, err
			}
			return struct{}{}, operation.call(ctx, s)
		}); err != nil {
			return nil, nil, err
		}
	}
	if err := author.Register(registry, toolMethod(), func(ctx context.Context, _ plugin.Request, v toolInput) (struct{}, error) {
		s, err := with(v.ID)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, s.SendToolResult(ctx, v.CallID, v.Result)
	}); err != nil {
		return nil, nil, err
	}
	if err := author.Register(registry, receiveMethod(), func(ctx context.Context, _ plugin.Request, v sessionID) (domain.Event, error) {
		s, err := with(v.ID)
		if err != nil {
			return domain.Event{}, err
		}
		return s.Receive(ctx)
	}); err != nil {
		return nil, nil, err
	}
	if err := author.Register(registry, idMethod("clear_input_audio"), func(ctx context.Context, _ plugin.Request, v sessionID) (struct{}, error) {
		s, err := with(v.ID)
		if err != nil {
			return struct{}{}, err
		}
		control, ok := s.(domain.AudioControl)
		if !ok {
			return struct{}{}, plugin.ErrUnsupported
		}
		return struct{}{}, control.ClearInputAudio(ctx)
	}); err != nil {
		return nil, nil, err
	}
	if err := author.Register(registry, truncationMethod(), func(ctx context.Context, _ plugin.Request, v truncationInput) (struct{}, error) {
		s, err := with(v.ID)
		if err != nil {
			return struct{}{}, err
		}
		control, ok := s.(domain.AudioControl)
		if !ok {
			return struct{}{}, plugin.ErrUnsupported
		}
		return struct{}{}, control.TruncateAudio(ctx, v.ItemID, v.ContentIndex, v.PlayedMS)
	}); err != nil {
		return nil, nil, err
	}
	if err := author.Register(registry, idMethod("close"), func(_ context.Context, _ plugin.Request, v sessionID) (struct{}, error) {
		g.mu.Lock()
		if g.session == nil || v.ID != g.id {
			g.mu.Unlock()
			return struct{}{}, plugin.ErrDenied
		}
		s := g.session
		g.session = nil
		g.id = ""
		g.mu.Unlock()
		return struct{}{}, s.Close()
	}); err != nil {
		return nil, nil, err
	}
	descriptor := registry.Descriptor()
	return &descriptor, g, nil
}

func (g *Guest) closeSession() error {
	g.mu.Lock()
	s := g.session
	g.session = nil
	g.id = ""
	g.mu.Unlock()
	if s != nil {
		return s.Close()
	}
	return nil
}

// Client opens a model conversation through an already admitted Ext session.
type Client struct{ caller author.Caller }

func NewClient(caller author.Caller) *Client { return &Client{caller: caller} }

func (c *Client) Open(ctx context.Context, request domain.OpenRequest) (*SessionClient, error) {
	if c == nil || c.caller == nil {
		return nil, plugin.ErrInvalid
	}
	result, err := author.Call(ctx, c.caller, openMethod(), request)
	if err != nil {
		return nil, err
	}
	if err := validateID(result); err != nil {
		return nil, err
	}
	return &SessionClient{caller: c.caller, id: result.ID}, nil
}

// SessionClient implements the portable realtime.Session and AudioControl.
type SessionClient struct {
	caller author.Caller
	id     string
}

func (c *SessionClient) SendText(ctx context.Context, value string) error {
	_, err := author.Call(ctx, c.caller, textMethod(), textInput{ID: c.id, Text: value})
	return err
}
func (c *SessionClient) SendAudio(ctx context.Context, value []byte) error {
	_, err := author.Call(ctx, c.caller, audioMethod(), audioInput{ID: c.id, Audio: value})
	return err
}
func (c *SessionClient) CommitAudio(ctx context.Context) error {
	_, err := author.Call(ctx, c.caller, idMethod("commit_audio"), sessionID{c.id})
	return err
}
func (c *SessionClient) Respond(ctx context.Context) error {
	_, err := author.Call(ctx, c.caller, idMethod("respond"), sessionID{c.id})
	return err
}
func (c *SessionClient) Interrupt(ctx context.Context) error {
	_, err := author.Call(ctx, c.caller, idMethod("interrupt"), sessionID{c.id})
	return err
}
func (c *SessionClient) SendToolResult(ctx context.Context, callID string, result []byte) error {
	_, err := author.Call(ctx, c.caller, toolMethod(), toolInput{ID: c.id, CallID: callID, Result: result})
	return err
}
func (c *SessionClient) Receive(ctx context.Context) (domain.Event, error) {
	return author.Call(ctx, c.caller, receiveMethod(), sessionID{c.id})
}
func (c *SessionClient) ClearInputAudio(ctx context.Context) error {
	_, err := author.Call(ctx, c.caller, idMethod("clear_input_audio"), sessionID{c.id})
	return err
}
func (c *SessionClient) TruncateAudio(ctx context.Context, itemID string, contentIndex, playedMS int) error {
	_, err := author.Call(ctx, c.caller, truncationMethod(), truncationInput{ID: c.id, ItemID: itemID, ContentIndex: contentIndex, PlayedMS: playedMS})
	return err
}
func (c *SessionClient) CloseContext(ctx context.Context) error {
	_, err := author.Call(ctx, c.caller, idMethod("close"), sessionID{c.id})
	return err
}
func (c *SessionClient) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.CloseContext(ctx)
}

var _ domain.Session = (*SessionClient)(nil)
var _ domain.AudioControl = (*SessionClient)(nil)
