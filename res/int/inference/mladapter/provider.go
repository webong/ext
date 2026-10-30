// Package mladapter adapts res/int/model (low-level tensor inference) into
// res/int/inference (high-level generation, embeddings, transcription, synthesis).
package mladapter

import (
	"context"
	"sync"

	"github.com/webong/ext/res/int/inference"
	ml "github.com/webong/ext/res/int/model"
)

// MLProvider wraps an ml.Provider as an inference.Provider. Each supported
// operation is bound to a model reference and a pair of pre/post processors.
// Operations that are not bound return inference.ErrUnsupported.
//
// The wrapped ml.Provider and ml.Store are owned by the caller. Close only
// releases sessions created by this adapter.
type MLProvider struct {
	mu        sync.Mutex
	store     *ml.Store
	ml        ml.Provider
	info      inference.ProviderInfo
	gen       *boundOp[inference.GenerationRequest, inference.GenerationResponse]
	embed     *boundOp[inference.EmbeddingRequest, inference.EmbeddingResponse]
	transcribe *boundOp[inference.TranscriptionRequest, inference.TranscriptionResponse]
	synthesize *boundOp[inference.SpeechRequest, inference.SpeechStream]
	sessions  map[string]*mlSession
}

// mlSession holds an ml.Session loaded for a specific model reference.
type mlSession struct {
	session ml.Session
	model   *ml.InstalledModel
}

// NewMLProvider wraps p as an inference.Provider backed by store for model
// resolution. info is the provider metadata reported by Info. No operations
// are supported until BindGenerate, BindEmbed, BindTranscribe, or
// BindSynthesize is called.
func NewMLProvider(store *ml.Store, p ml.Provider, info inference.ProviderInfo) *MLProvider {
	if err := info.Validate(); err != nil {
		return nil
	}
	return &MLProvider{
		store:    store,
		ml:       p,
		info:     info,
		sessions: map[string]*mlSession{},
	}
}

// BindGenerate binds the Generate operation to an ml model. pre converts a
// GenerationRequest into ml tensors; post converts output tensors into a
// GenerationResponse.
func (a *MLProvider) BindGenerate(ref ml.Reference, pre Preprocessor[inference.GenerationRequest], post Postprocessor[inference.GenerationResponse]) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gen = &boundOp[inference.GenerationRequest, inference.GenerationResponse]{
		ref:  ref,
		pre:  pre,
		post: post,
	}
}

// BindEmbed binds the Embed operation to an ml model.
func (a *MLProvider) BindEmbed(ref ml.Reference, pre Preprocessor[inference.EmbeddingRequest], post Postprocessor[inference.EmbeddingResponse]) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.embed = &boundOp[inference.EmbeddingRequest, inference.EmbeddingResponse]{
		ref:  ref,
		pre:  pre,
		post: post,
	}
}

// BindTranscribe binds the Transcribe operation to an ml model.
func (a *MLProvider) BindTranscribe(ref ml.Reference, pre Preprocessor[inference.TranscriptionRequest], post Postprocessor[inference.TranscriptionResponse]) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.transcribe = &boundOp[inference.TranscriptionRequest, inference.TranscriptionResponse]{
		ref:  ref,
		pre:  pre,
		post: post,
	}
}

// BindSynthesize binds the Synthesize operation to an ml model. Synthesis
// produces a stream of audio bytes.
func (a *MLProvider) BindSynthesize(ref ml.Reference, pre Preprocessor[inference.SpeechRequest], post Postprocessor[inference.SpeechStream]) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.synthesize = &boundOp[inference.SpeechRequest, inference.SpeechStream]{
		ref:  ref,
		pre:  pre,
		post: post,
	}
}

// Info returns provider metadata.
func (a *MLProvider) Info(ctx context.Context) (inference.ProviderInfo, error) {
	if err := ctx.Err(); err != nil {
		return inference.ProviderInfo{}, err
	}
	return a.info, nil
}

// Generate runs one generation pass through the bound ml model.
func (a *MLProvider) Generate(ctx context.Context, req inference.GenerationRequest) (inference.GenerationResponse, error) {
	if err := ctx.Err(); err != nil {
		return inference.GenerationResponse{}, err
	}
	a.mu.Lock()
	op := a.gen
	a.mu.Unlock()
	if op == nil || op.pre == nil || op.post == nil {
		return inference.GenerationResponse{}, inference.ErrUnsupported
	}
	return op.run(ctx, a, req)
}

// Stream is not supported by raw ml tensor inference. Use Streamer only via
// a remote provider adapter that supports streaming.
func (a *MLProvider) Stream(ctx context.Context, req inference.GenerationRequest, sink inference.EventSink) (inference.GenerationResponse, error) {
	if err := ctx.Err(); err != nil {
		return inference.GenerationResponse{}, err
	}
	return inference.GenerationResponse{}, inference.ErrUnsupported
}

// Embed returns embeddings for the inputs through the bound ml model.
func (a *MLProvider) Embed(ctx context.Context, req inference.EmbeddingRequest) (inference.EmbeddingResponse, error) {
	if err := ctx.Err(); err != nil {
		return inference.EmbeddingResponse{}, err
	}
	a.mu.Lock()
	op := a.embed
	a.mu.Unlock()
	if op == nil || op.pre == nil || op.post == nil {
		return inference.EmbeddingResponse{}, inference.ErrUnsupported
	}
	return op.run(ctx, a, req)
}

// Transcribe transcribes audio through the bound ml model.
func (a *MLProvider) Transcribe(ctx context.Context, req inference.TranscriptionRequest) (inference.TranscriptionResponse, error) {
	if err := ctx.Err(); err != nil {
		return inference.TranscriptionResponse{}, err
	}
	a.mu.Lock()
	op := a.transcribe
	a.mu.Unlock()
	if op == nil || op.pre == nil || op.post == nil {
		return inference.TranscriptionResponse{}, inference.ErrUnsupported
	}
	return op.run(ctx, a, req)
}

// Synthesize synthesizes text to audio through the bound ml model.
func (a *MLProvider) Synthesize(ctx context.Context, req inference.SpeechRequest) (inference.SpeechStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	op := a.synthesize
	a.mu.Unlock()
	if op == nil || op.pre == nil || op.post == nil {
		return nil, inference.ErrUnsupported
	}
	session, err := a.sessionFor(ctx, op.ref)
	if err != nil {
		return nil, err
	}
	inputs, err := op.pre.Preprocess(ctx, req)
	if err != nil {
		return nil, err
	}
	outputs, err := session.session.Infer(ctx, inputs)
	if err != nil {
		return nil, err
	}
	return op.post.Postprocess(ctx, outputs)
}

// Models is not supported by raw ml provider metadata. Use ModelLister only
// via a remote provider adapter that supports model listing.
func (a *MLProvider) Models(ctx context.Context) ([]inference.ModelInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, inference.ErrUnsupported
}

// Close releases all sessions owned by this adapter. It does not close the
// wrapped ml.Provider or ml.Store (the caller owns them).
func (a *MLProvider) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var firstErr error
	for key, s := range a.sessions {
		if err := s.session.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(a.sessions, key)
	}
	return firstErr
}

// sessionFor loads the ml model for ref and binds a session, reusing an
// existing session for the same model reference.
func (a *MLProvider) sessionFor(ctx context.Context, ref ml.Reference) (*mlSession, error) {
	key := ref.CanonicalKey()
	a.mu.Lock()
	if s, ok := a.sessions[key]; ok {
		a.mu.Unlock()
		return s, nil
	}
	a.mu.Unlock()
	model, err := a.store.Resolve(ctx, ref.Provider, ref.Name, ref.Version)
	if err != nil {
		return nil, err
	}
	session, err := a.ml.Load(ctx, model)
	if err != nil {
		return nil, err
	}
	s := &mlSession{session: session, model: model}
	a.mu.Lock()
	// Check again in case another goroutine loaded it.
	if existing, ok := a.sessions[key]; ok {
		a.mu.Unlock()
		_ = session.Close()
		return existing, nil
	}
	a.sessions[key] = s
	a.mu.Unlock()
	return s, nil
}
