// Package inference guest-side helpers register a res/inference.Provider as an
// Ext plugin guest serving inference.provider/v1.
package inference

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/author"
	"github.com/webong/ext/pkg/ext-go/engine/guest/stream"
	"github.com/webong/ext/res/inference"
)

// health is the provider health report.
type health struct {
	Status string `json:"status"`
	Ready  bool   `json:"ready"`
}

// streamOpenParams are the parameters for opening an ext.stream via the
// stream service. The id refers to a reader owned by the inference provider.
type streamOpenParams struct {
	Op   string          `json:"op"`   // "stream" or "synthesize"
	ID   string          `json:"id"`   // reader id owned by the provider
	Data json.RawMessage `json:"data"` // request payload (generation or speech request)
}

// Provider adapts a res/inference.Provider to the plugin guest contract. The
// guest declares the full inference.provider/v1 surface and routes each
// operation to the provider. A provider returns inference.ErrUnsupported for
// any operation it does not advertise.
type Provider struct {
	provider inference.Provider
	streams  *stream.Service

	mu           sync.Mutex
	genStreams   map[string]*genReader
	synthStreams map[string]*synthReader
}

// RegisterProvider registers all inference.provider/v1 operations on the
// author registry and returns the plugin Descriptor. The authorize callback
// is required by the engine and must enforce the host's policy. RegisterProvider
// creates an internal ext.stream service for stream and synthesize operations.
func RegisterProvider(r *author.Registry, p inference.Provider, authorize func(context.Context, plugin.Request) error) (*plugin.Descriptor, error) {
	if p == nil || r == nil || authorize == nil {
		return nil, plugin.ErrInvalid
	}
	g := &Provider{
		provider:     p,
		genStreams:   map[string]*genReader{},
		synthStreams: map[string]*synthReader{},
	}

	st, err := stream.New(stream.Options{
		Capacity: 128,
		MaxAge:   5 * time.Minute,
		Scope: func(ctx context.Context, r plugin.Request) (string, error) {
			return "inference.provider/v1", nil
		},
		Open: g.openReader,
	})
	if err != nil {
		return nil, err
	}
	g.streams = st
	if err := st.Register(r); err != nil {
		return nil, err
	}

	if err := author.Register(r, capabilitiesMethod(), g.capabilities); err != nil {
		return nil, err
	}
	if err := author.Register(r, healthMethod(), g.health); err != nil {
		return nil, err
	}
	if err := author.Register(r, generateMethod(), g.generate); err != nil {
		return nil, err
	}
	if err := author.Register(r, streamMethod(), g.openStream); err != nil {
		return nil, err
	}
	if err := author.Register(r, embedMethod(), g.embed); err != nil {
		return nil, err
	}
	if err := author.Register(r, transcribeMethod(), g.transcribe); err != nil {
		return nil, err
	}
	if err := author.Register(r, synthesizeMethod(), g.openSynthesize); err != nil {
		return nil, err
	}
	if err := author.Register(r, modelsMethod(), g.models); err != nil {
		return nil, err
	}
	if err := author.Register(r, closeMethod(), g.closeOp); err != nil {
		return nil, err
	}

	d := r.Descriptor()
	return &d, nil
}

// openReader is the ext.stream Open callback. It looks up a reader owned by the
// inference provider and returns it for reading via the ext.stream client.
func (g *Provider) openReader(ctx context.Context, r plugin.Request, params json.RawMessage) (stream.Reader, error) {
	var op streamOpenParams
	if err := json.Unmarshal(params, &op); err != nil {
		return nil, &plugin.RemoteError{Code: "invalid_input", Message: "stream open parameters must be valid JSON"}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	switch op.Op {
	case OpStream:
		reader, ok := g.genStreams[op.ID]
		if !ok {
			return nil, &plugin.RemoteError{Code: "not_found", Message: "generation stream not found"}
		}
		delete(g.genStreams, op.ID)
		go reader.run(ctx)
		return reader, nil
	case OpSynthesize:
		reader, ok := g.synthStreams[op.ID]
		if !ok {
			return nil, &plugin.RemoteError{Code: "not_found", Message: "synthesis stream not found"}
		}
		delete(g.synthStreams, op.ID)
		go reader.run(ctx)
		return reader, nil
	default:
		return nil, &plugin.RemoteError{Code: "invalid_input", Message: "stream op must be stream or synthesize"}
	}
}

// openStream is the inference.provider/v1 stream operation handler. It starts
// a generation stream and returns a stream id that the host can open via ext.stream.
func (g *Provider) openStream(ctx context.Context, _ plugin.Request, req inference.GenerationRequest) (stream.OpenResponse, error) {
	str, ok := g.provider.(interface {
		Stream(context.Context, inference.GenerationRequest, inference.EventSink) (inference.GenerationResponse, error)
	})
	if !ok {
		return stream.OpenResponse{}, inference.ErrUnsupported
	}
	reader := newGenReader()
	id := newStreamID()
	reader.run = func(ctx context.Context) {
		sink := inference.SinkFunc(func(ctx context.Context, ev inference.GenerationEvent) error {
			return reader.push(ctx, ev)
		})
		_, err := str.Stream(ctx, req, sink)
		reader.finish(pluginError(err))
	}
	g.mu.Lock()
	if len(g.genStreams) >= 128 {
		g.mu.Unlock()
		return stream.OpenResponse{}, inference.ErrResourceExhausted
	}
	g.genStreams[id] = reader
	g.mu.Unlock()
	time.AfterFunc(5*time.Minute, func() { g.mu.Lock(); delete(g.genStreams, id); g.mu.Unlock() })
	return stream.OpenResponse{ID: id}, nil
}

// openSynthesize is the inference.provider/v1 synthesize operation handler. It
// starts a synthesis stream and returns a stream id that the host can open via ext.stream.
func (g *Provider) openSynthesize(ctx context.Context, _ plugin.Request, req inference.SpeechRequest) (stream.OpenResponse, error) {
	syn, ok := g.provider.(interface {
		Synthesize(context.Context, inference.SpeechRequest) (inference.SpeechStream, error)
	})
	if !ok {
		return stream.OpenResponse{}, inference.ErrUnsupported
	}
	reader := newSynthReader()
	id := newStreamID()
	reader.run = func(ctx context.Context) {
		ss, err := syn.Synthesize(ctx, req)
		if err != nil {
			reader.finish(pluginError(err))
			return
		}
		defer ss.Close()
		stop := context.AfterFunc(ctx, func() { _ = ss.Close() })
		defer stop()
		buf := make([]byte, 64<<10)
		for {
			n, err := ss.Read(buf)
			if n > 0 {
				if pushErr := reader.push(ctx, buf[:n]); pushErr != nil {
					reader.finish(pushErr)
					return
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					reader.finish(nil)
				} else {
					reader.finish(pluginError(err))
				}
				return
			}
		}
	}
	g.mu.Lock()
	if len(g.synthStreams) >= 128 {
		g.mu.Unlock()
		return stream.OpenResponse{}, inference.ErrResourceExhausted
	}
	g.synthStreams[id] = reader
	g.mu.Unlock()
	time.AfterFunc(5*time.Minute, func() { g.mu.Lock(); delete(g.synthStreams, id); g.mu.Unlock() })
	return stream.OpenResponse{ID: id}, nil
}

func newStreamID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString(b[:])
	}
	return hex.EncodeToString(b[:])
}

func (g *Provider) capabilities(ctx context.Context, _ plugin.Request, _ struct{}) (inference.ProviderInfo, error) {
	return g.provider.Info(ctx)
}

func (g *Provider) health(ctx context.Context, _ plugin.Request, _ struct{}) (health, error) {
	if _, err := g.provider.Info(ctx); err != nil {
		return health{Status: "unhealthy", Ready: false}, err
	}
	return health{Status: "healthy", Ready: true}, nil
}

func (g *Provider) generate(ctx context.Context, _ plugin.Request, req inference.GenerationRequest) (inference.GenerationResponse, error) {
	gen, ok := g.provider.(interface {
		Generate(context.Context, inference.GenerationRequest) (inference.GenerationResponse, error)
	})
	if !ok {
		return inference.GenerationResponse{}, inference.ErrUnsupported
	}
	resp, err := gen.Generate(ctx, req)
	return resp, pluginError(err)
}

func (g *Provider) embed(ctx context.Context, _ plugin.Request, req inference.EmbeddingRequest) (inference.EmbeddingResponse, error) {
	emb, ok := g.provider.(interface {
		Embed(context.Context, inference.EmbeddingRequest) (inference.EmbeddingResponse, error)
	})
	if !ok {
		return inference.EmbeddingResponse{}, inference.ErrUnsupported
	}
	resp, err := emb.Embed(ctx, req)
	return resp, pluginError(err)
}

func (g *Provider) transcribe(ctx context.Context, _ plugin.Request, req inference.TranscriptionRequest) (inference.TranscriptionResponse, error) {
	tr, ok := g.provider.(interface {
		Transcribe(context.Context, inference.TranscriptionRequest) (inference.TranscriptionResponse, error)
	})
	if !ok {
		return inference.TranscriptionResponse{}, inference.ErrUnsupported
	}
	resp, err := tr.Transcribe(ctx, req)
	return resp, pluginError(err)
}

func (g *Provider) models(ctx context.Context, _ plugin.Request, _ struct{}) ([]inference.ModelInfo, error) {
	ml, ok := g.provider.(interface {
		ListModels(context.Context) ([]inference.ModelInfo, error)
	})
	if !ok {
		return nil, inference.ErrUnsupported
	}
	resp, err := ml.ListModels(ctx)
	return resp, pluginError(err)
}

func pluginError(err error) error {
	if err == nil {
		return nil
	}
	var remote *inference.RemoteError
	if errors.As(err, &remote) {
		return &plugin.RemoteError{Code: remote.Code, Message: remote.Message}
	}
	for _, category := range []struct {
		err  error
		code string
	}{
		{inference.ErrAuthentication, "authentication"}, {inference.ErrAuthorization, "authorization"},
		{inference.ErrRateLimit, "rateLimit"}, {inference.ErrTimeout, "timeout"},
		{inference.ErrCanceled, "canceled"}, {inference.ErrUnavailable, "unavailable"},
		{inference.ErrUnsupported, "unsupported"}, {inference.ErrInvalidRequest, "invalidRequest"},
		{inference.ErrInvalidResponse, "invalidResponse"}, {inference.ErrResourceExhausted, "resourceExhausted"},
		{inference.ErrIncompatibleModel, "incompatibleModel"}, {inference.ErrTransientNative, "transientNative"},
	} {
		if errors.Is(err, category.err) {
			return &plugin.RemoteError{Code: category.code, Message: category.err.Error()}
		}
	}
	return &plugin.RemoteError{Code: "unavailable", Message: "provider operation failed"}
}

func (g *Provider) closeOp(ctx context.Context, _ plugin.Request, _ struct{}) (struct{}, error) {
	_ = g.streams.Close()
	if err := g.provider.Close(); err != nil {
		return struct{}{}, err
	}
	return struct{}{}, nil
}

var _ = errors.Is
