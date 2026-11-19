// Package client implements the OpenAI-compatible HTTP inference provider
// shared by the OpenAI and Ollama adapter guests.
package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	inference "github.com/webong/ext/res/int/inference"
)

// Provider is an OpenAI-compatible inference provider.
type Provider struct {
	id       string
	name     string
	endpoint string
	apiKey   string
	client   *http.Client
	// audio is whether the endpoint serves /audio/speech and /audio/transcriptions;
	// OpenAI does, and an OpenAI-compatible server says so with EnableAudio.
	audio bool
}

// NewProvider creates an OpenAI provider. endpoint is the base URL (for
// example "https://api.openai.com/v1"). apiKey is the bearer token.
func NewProvider(endpoint, apiKey string) (*Provider, error) {
	p, err := NewCompatibleProvider("openai", "OpenAI", endpoint, apiKey)
	if err != nil {
		return nil, err
	}
	p.EnableAudio()
	return p, nil
}

// EnableAudio declares that the endpoint serves OpenAI's speech and transcription APIs, so
// Synthesize and Transcribe use them instead of returning ErrUnsupported.
func (p *Provider) EnableAudio() { p.audio = true }

// NewCompatibleProvider constructs an explicitly configured OpenAI-compatible
// provider. The caller selects its identity and whether it uses a bearer key.
func NewCompatibleProvider(id, name, endpoint, apiKey string) (*Provider, error) {
	if id == "" || name == "" {
		return nil, inference.ErrInvalidRequest
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid endpoint %q", inference.ErrInvalidRequest, endpoint)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%w: endpoint scheme must be https or http", inference.ErrInvalidRequest)
	}
	return &Provider{
		id:       id,
		name:     name,
		endpoint: strings.TrimRight(endpoint, "/"),
		apiKey:   apiKey,
		client: &http.Client{
			Timeout: 120 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse // redirects disabled by default
			},
		},
	}, nil
}

// Info returns provider metadata.
func (p *Provider) Info(ctx context.Context) (inference.ProviderInfo, error) {
	if err := ctx.Err(); err != nil {
		return inference.ProviderInfo{}, err
	}
	modalities := []inference.Modality{inference.ModalityText}
	capabilities := []inference.Capability{
		inference.CapabilityGenerate,
		inference.CapabilityStream,
		inference.CapabilityEmbed,
		inference.CapabilityToolProposal,
		inference.CapabilityModelListing,
	}
	if p.audio {
		modalities = append(modalities, inference.ModalityAudio)
		capabilities = append(capabilities, inference.CapabilitySynthesize, inference.CapabilityTranscribe)
	}
	return inference.ProviderInfo{
		ID:           p.id,
		Name:         p.name,
		Version:      "1.0.0",
		Stability:    inference.StabilityStable,
		Modalities:   modalities,
		Capabilities: capabilities,
		Limits: inference.Limits{
			MaxOutputTokens: 4096,
			MaxInputTokens:  128000,
			MaxBytes:        32 << 20,
		},
		Protocol: map[string]string{"endpoint": p.endpoint, "auth": p.authKind()},
	}, nil
}

// Close releases the provider. It is idempotent and safe to call after an error.
func (p *Provider) Close() error {
	return nil
}

// Generate runs one non-streaming generation pass via the OpenAI Chat Completions API.
func (p *Provider) Generate(ctx context.Context, req inference.GenerationRequest) (inference.GenerationResponse, error) {
	if err := req.Validate(); err != nil {
		return inference.GenerationResponse{}, err
	}
	body, err := marshalChatRequest(req, false)
	if err != nil {
		return inference.GenerationResponse{}, err
	}
	respBody, err := p.post(ctx, "/chat/completions", body)
	if err != nil {
		return inference.GenerationResponse{}, err
	}
	return parseChatResponse(respBody)
}

// Stream streams generation events via the OpenAI Chat Completions API with stream: true.
func (p *Provider) Stream(ctx context.Context, req inference.GenerationRequest, sink inference.EventSink) (inference.GenerationResponse, error) {
	if err := req.Validate(); err != nil {
		return inference.GenerationResponse{}, err
	}
	body, err := marshalChatRequest(req, true)
	if err != nil {
		return inference.GenerationResponse{}, err
	}
	return p.streamEvents(ctx, "/chat/completions", body, sink)
}

// Embed returns embeddings via the OpenAI Embeddings API.
func (p *Provider) Embed(ctx context.Context, req inference.EmbeddingRequest) (inference.EmbeddingResponse, error) {
	if err := req.Validate(); err != nil {
		return inference.EmbeddingResponse{}, err
	}
	body, err := marshalEmbedRequest(req)
	if err != nil {
		return inference.EmbeddingResponse{}, err
	}
	respBody, err := p.post(ctx, "/embeddings", body)
	if err != nil {
		return inference.EmbeddingResponse{}, err
	}
	return parseEmbedResponse(respBody)
}

// Synthesize streams speech from OpenAI's /audio/speech API. The returned stream reads the
// response body as it arrives, bounded at 64 MiB.
func (p *Provider) Synthesize(ctx context.Context, req inference.SpeechRequest) (inference.SpeechStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.audio {
		return nil, inference.ErrUnsupported
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	body, err := marshalSpeechRequest(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/audio/speech", bytesReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", inference.ErrInvalidRequest, err)
	}
	p.authorize(httpReq)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, transportError(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, mapOpenAIError(raw, resp.StatusCode)
	}
	return &speechStream{body: resp.Body, r: io.LimitReader(resp.Body, 64<<20)}, nil
}

// Transcribe transcribes audio with OpenAI's /audio/transcriptions API.
func (p *Provider) Transcribe(ctx context.Context, req inference.TranscriptionRequest) (inference.TranscriptionResponse, error) {
	if err := ctx.Err(); err != nil {
		return inference.TranscriptionResponse{}, err
	}
	if !p.audio {
		return inference.TranscriptionResponse{}, inference.ErrUnsupported
	}
	if err := req.Validate(); err != nil {
		return inference.TranscriptionResponse{}, err
	}
	body, contentType, err := marshalTranscriptionRequest(req)
	if err != nil {
		return inference.TranscriptionResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/audio/transcriptions", bytes.NewReader(body))
	if err != nil {
		return inference.TranscriptionResponse{}, fmt.Errorf("%w: %v", inference.ErrInvalidRequest, err)
	}
	p.authorize(httpReq)
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return inference.TranscriptionResponse{}, transportError(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
	if err != nil {
		return inference.TranscriptionResponse{}, inference.NewRemoteError(inference.ErrInvalidResponse, "failed to read response", "openai")
	}
	if len(raw) > 32<<20 {
		return inference.TranscriptionResponse{}, inference.NewRemoteError(inference.ErrResourceExhausted, "response too large", "openai")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return inference.TranscriptionResponse{}, mapOpenAIError(raw, resp.StatusCode)
	}
	return parseTranscription(raw)
}

// speechStream is the body of a speech response, read as it arrives.
type speechStream struct {
	body io.Closer
	r    io.Reader
	once sync.Once
}

func (s *speechStream) Read(p []byte) (int, error) { return s.r.Read(p) }

func (s *speechStream) Close() error {
	var err error
	s.once.Do(func() { err = s.body.Close() })
	return err
}

// Models lists OpenAI models.
func (p *Provider) ListModels(ctx context.Context) ([]inference.ModelInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	respBody, err := p.get(ctx, "/models")
	if err != nil {
		return nil, err
	}
	return parseModelList(respBody)
}

func (p *Provider) get(ctx context.Context, path string) ([]byte, error) {
	return p.do(ctx, http.MethodGet, path, nil)
}

func (p *Provider) post(ctx context.Context, path string, body []byte) ([]byte, error) {
	return p.do(ctx, http.MethodPost, path, body)
}

func (p *Provider) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctx.Err()
	}
	req, err := http.NewRequestWithContext(ctx, method, p.endpoint+path, bytesReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", inference.ErrInvalidRequest, err)
	}
	p.authorize(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, transportError(err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
	if err != nil {
		return nil, inference.NewRemoteError(inference.ErrInvalidResponse, "failed to read response", "openai")
	}
	if len(raw) > 32<<20 {
		return nil, inference.NewRemoteError(inference.ErrResourceExhausted, "response too large", "openai")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, mapOpenAIError(raw, resp.StatusCode)
	}
	return raw, nil
}

func (p *Provider) authorize(req *http.Request) {
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
}

// transportError maps a failed request without leaking its URL or credentials.
func transportError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return inference.NewRemoteError(inference.ErrCanceled, "operation canceled", "openai")
	}
	return inference.NewRemoteError(inference.ErrUnavailable, "request failed", "openai")
}

func (p *Provider) authKind() string {
	if p.apiKey == "" {
		return "none"
	}
	return "bearer"
}

// bytesReader returns an io.Reader for body, or nil if body is empty.
func bytesReader(body []byte) io.Reader {
	if len(body) == 0 {
		return nil
	}
	return strings.NewReader(string(body))
}
