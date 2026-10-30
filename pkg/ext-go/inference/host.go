// Package inference host-side helpers call inference.provider/v1 operations
// through a plugin session.
package inference

import (
	"context"
	"encoding/json"
	"errors"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/author"
	"github.com/webong/ext/pkg/ext-go/engine/guest/stream"
	"github.com/webong/ext/res/int/inference"
)

// Client calls inference.provider/v1 operations on a plugin session.
type Client struct {
	caller author.Caller
}

// NewClient wraps a plugin session caller as an inference provider client.
func NewClient(caller author.Caller) *Client {
	return &Client{caller: caller}
}

// Capabilities returns the provider's metadata.
func (c *Client) Capabilities(ctx context.Context) (inference.ProviderInfo, error) {
	return author.Call(ctx, c.caller, capabilitiesMethod(), struct{}{})
}

// Health reports whether the provider is healthy and ready.
func (c *Client) Health(ctx context.Context) (health, error) {
	return author.Call(ctx, c.caller, healthMethod(), struct{}{})
}

// Generate runs one generation pass and returns the complete response.
func (c *Client) Generate(ctx context.Context, req inference.GenerationRequest) (inference.GenerationResponse, error) {
	resp, err := author.Call(ctx, c.caller, generateMethod(), req)
	return resp, inferenceError(err)
}

// Stream opens an ext.stream of GenerationEvent and returns its reader. The
// returned stream.Client reads from the provider's generation stream via the
// ext.stream protocol.
func (c *Client) Stream(ctx context.Context, req inference.GenerationRequest) (*stream.Client, error) {
	resp, err := author.Call(ctx, c.caller, streamMethod(), req)
	if err != nil {
		return nil, inferenceError(err)
	}
	params := streamOpenParams{Op: OpStream, ID: resp.ID}
	data, err := json.Marshal(params)
	if err != nil {
		return nil, inferenceError(err)
	}
	return stream.Open(ctx, c.caller, data)
}

// Embed returns embeddings for the inputs.
func (c *Client) Embed(ctx context.Context, req inference.EmbeddingRequest) (inference.EmbeddingResponse, error) {
	resp, err := author.Call(ctx, c.caller, embedMethod(), req)
	return resp, inferenceError(err)
}

// Transcribe transcribes audio.
func (c *Client) Transcribe(ctx context.Context, req inference.TranscriptionRequest) (inference.TranscriptionResponse, error) {
	resp, err := author.Call(ctx, c.caller, transcribeMethod(), req)
	return resp, inferenceError(err)
}

// Synthesize opens an ext.stream of audio bytes and returns its reader.
func (c *Client) Synthesize(ctx context.Context, req inference.SpeechRequest) (*stream.Client, error) {
	resp, err := author.Call(ctx, c.caller, synthesizeMethod(), req)
	if err != nil {
		return nil, err
	}
	params := streamOpenParams{Op: OpSynthesize, ID: resp.ID}
	data, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return stream.Open(ctx, c.caller, data)
}

// Models lists the models the provider can serve.
func (c *Client) Models(ctx context.Context) ([]inference.ModelInfo, error) {
	resp, err := author.Call(ctx, c.caller, modelsMethod(), struct{}{})
	return resp, inferenceError(err)
}

func inferenceError(err error) error {
	if err == nil {
		return nil
	}
	var remote *plugin.RemoteError
	if errors.As(err, &remote) && (remote.Code == "invalidResponse" || inference.Category(remote.Code) != inference.ErrInvalidResponse) {
		return &inference.RemoteError{Code: remote.Code, Message: remote.Message}
	}
	return err
}

// Close releases the provider.
func (c *Client) Close(ctx context.Context) error {
	_, err := author.Call(ctx, c.caller, closeMethod(), struct{}{})
	return err
}
