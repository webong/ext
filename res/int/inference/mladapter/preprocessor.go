// Package mladapter adapts res/int/model (low-level tensor inference) into
// res/int/inference (high-level generation, embeddings, transcription, synthesis).
//
// res/int/model owns raw tensor inference through a caller-selected provider.
// res/int/inference owns the portable, transport-free inference contract. This
// package bridges them: it wraps an ml.Provider as an inference.Provider,
// applying explicit preprocessing and postprocessing for each operation.
//
// Raw tensor inference must not pretend automatically to be text generation
// or content interpretation. Each operation requires an explicit preprocessor
// (request → tensors) and postprocessor (tensors → response).
package mladapter

import (
	"context"

	ml "github.com/webong/ext/res/int/model"
)

// Preprocessor converts a res/int/inference request into res/int/model tensors. The
// implementation is responsible for model-specific encoding (tokenization,
// feature extraction, etc.) and must honor ctx for cancellation.
type Preprocessor[Req any] interface {
	Preprocess(ctx context.Context, req Req) ([]ml.Tensor, error)
}

// Postprocessor converts res/int/model output tensors into a res/int/inference response.
// The implementation is responsible for model-specific decoding (detokenization,
// embedding normalization, etc.) and must honor ctx for cancellation.
type Postprocessor[Resp any] interface {
	Postprocess(ctx context.Context, outputs []ml.Tensor) (Resp, error)
}

// tensorPreprocessor is a Preprocessor backed by a function.
type tensorPreprocessor[Req any] struct {
	fn func(context.Context, Req) ([]ml.Tensor, error)
}

func (p *tensorPreprocessor[Req]) Preprocess(ctx context.Context, req Req) ([]ml.Tensor, error) {
	return p.fn(ctx, req)
}

// tensorPostprocessor is a Postprocessor backed by a function.
type tensorPostprocessor[Resp any] struct {
	fn func(context.Context, []ml.Tensor) (Resp, error)
}

func (p *tensorPostprocessor[Resp]) Postprocess(ctx context.Context, outputs []ml.Tensor) (Resp, error) {
	return p.fn(ctx, outputs)
}

// FuncPreprocessor builds a Preprocessor from a function.
func FuncPreprocessor[Req any](fn func(context.Context, Req) ([]ml.Tensor, error)) Preprocessor[Req] {
	return &tensorPreprocessor[Req]{fn: fn}
}

// FuncPostprocessor builds a Postprocessor from a function.
func FuncPostprocessor[Resp any](fn func(context.Context, []ml.Tensor) (Resp, error)) Postprocessor[Resp] {
	return &tensorPostprocessor[Resp]{fn: fn}
}

// boundOp binds one inference operation to an ml model and its pre/post
// processors. Zero value is unused (all operations unsupported).
type boundOp[Req any, Resp any] struct {
	ref   ml.Reference
	pre   Preprocessor[Req]
	post  Postprocessor[Resp]
}

// run executes one bound operation: resolve session, preprocess, infer,
// postprocess. It is a method on the generic boundOp type so the type
// parameters come from the type declaration (supported in Go 1.18+).
func (op *boundOp[Req, Resp]) run(ctx context.Context, a *MLProvider, req Req) (Resp, error) {
	var zero Resp
	session, err := a.sessionFor(ctx, op.ref)
	if err != nil {
		return zero, err
	}
	inputs, err := op.pre.Preprocess(ctx, req)
	if err != nil {
		return zero, err
	}
	outputs, err := session.session.Infer(ctx, inputs)
	if err != nil {
		return zero, err
	}
	return op.post.Postprocess(ctx, outputs)
}
