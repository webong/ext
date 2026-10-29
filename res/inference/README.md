# res/inference — portable inference resource

`res/inference` is the provider-neutral, transport-free inference layer used by
CTN and available to Cymonkey and future applications. It sits above `res/ml` (low-level local
model support) and below provider adapters (OpenAI, Anthropic, Gemini, Ollama,
OpenAI-compatible, ONNX Runtime, etc.).

```
res/ml
   │
   ▼
res/inference
   │
   ├── public transport-free contracts
   ├── local res/ml adaptation
   └── HTTP/client primitives where appropriate
   │
   ▼
plugin integration and provider adapters
   │
   ├── CTN
   └── future consumers, including Cymonkey
```

## Contract

The language-neutral contract is `inference.provider/v1`, recorded in
[docs/contract-names.md](../../docs/contract-names.md). It is served through the
Ext plugin contract (`ext.plugin/v1`) using the existing plugin host/guest
mechanics (`pkg/ext-go/engine`). A separate integration package,
`pkg/ext-go/inference`, bridges `res/inference` and the plugin engine so the
core never imports CTX, CTN, Cymonkey, or a concrete provider.
See [provider packaging and discovery](../../docs/inference-providers.md) for the
build, install, trust, and CTN flow.

## Capabilities

The `Provider` interface is split into small capability interfaces. A provider
implements only what it supports and advertises it in `ProviderInfo`:

- `Provider` — `Info`, `Close`
- `Generator` — `Generate`
- `Streamer` — `Stream` (through `EventSink`)
- `Embedder` — `Embed`
- `Transcriber` — `Transcribe`
- `Synthesizer` — `Synthesize`
- `ModelLister` — `ListModels` (optional)

## Operations

- generation (`Generate`, `Stream`)
- embeddings (`Embed`)
- transcription (`Transcribe`)
- speech synthesis (`Synthesize`)
- capabilities and health (`Info`)
- structured output (`OutputSchema` JSON Schema)
- tool-call proposals (`ToolDefinition` → `ToolCall`)
- multimodal inputs (text, image, audio, video, file)

## Errors

Stable, typed failures inspected with `errors.Is`:

`ErrAuthentication`, `ErrAuthorization`, `ErrRateLimit`, `ErrTimeout`,
`ErrCanceled`, `ErrUnavailable`, `ErrUnsupported`, `ErrInvalidRequest`,
`ErrInvalidResponse`, `ErrResourceExhausted`, `ErrIncompatibleModel`,
`ErrTransientNative`.

`RemoteError` carries a code, bounded message, optional retry-after delay, and
originating provider. Credentials and remote response bodies never appear in
errors.

## Module

- Module path: `github.com/webong/ext/res/inference`
- Go: 1.25.0
- No external dependencies (standard library only)

## Platform matrix

| Platform | Native support | Status |
|----------|---------------|--------|
| Apple | CoreML | `.mlmodel` float32 multi-array backend implemented; integration with a real model is still pending |
| Windows | ONNX Runtime CPU | optional C# backend implemented; Windows build and model integration remain to be verified on a Windows runner |
| Android | TensorFlow Lite | optional float32 backend implemented; host supplies the runtime; NNAPI remains a stub |
| Linux | external runtimes via adapters | portable contract; no built-in runtime |

No provider is documented as implemented unless it performs real inference.
Tools are proposed, never executed automatically.
