# res/inference — portable inference resource

`res/inference` is the provider-neutral, transport-free inference layer used by
CTN, Cymonkey, and future applications. It sits above `res/ml` (low-level local
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
   └── Cymonkey
```

## Contract

The language-neutral contract is `inference.provider/v1`, recorded in
[docs/contract-names.md](../docs/contract-names.md). It is served through the
Ext plugin contract (`ext.plugin/v1`) using the existing plugin host/guest
mechanics (`pkg/ext-go/engine`). A separate integration package,
`pkg/ext-go/inference`, bridges `res/inference` and the plugin engine so the
core never imports CTX, CTN, Cymonkey, or a concrete provider.

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
| Apple | CoreML, Vision, Speech, Sound Analysis, Natural Language | real inference |
| Windows | Windows ML / ONNX | buildable |
| Android | NNAPI (legacy/deprecated), TFLite/AICore optional adapters | device-reported |
| Linux | external runtimes via adapters | portable contract |

No provider is documented as implemented unless it performs real inference.
Tools are proposed, never executed automatically.
