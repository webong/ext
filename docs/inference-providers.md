# Inference provider packages

`res/int/inference` defines the portable provider contract. The reusable plugin
integration in `pkg/ext-go/inference` serves a provider guest and packages its
executable. The package store in `pkg/ext-go/engine/host/store` verifies sealed
packages, matches contracts, and selects the executable for the current
platform. The scoped trust ledger in `pkg/ext-go/engine/host/trust` pins reviewed
digests; `lib/plugin` implements the same format for other host languages.
ext adapters, CTN inference providers, and Ext commands use separate scopes.

## Build, install, and trust

The bundled Go guests are `adapters/openai`, `adapters/ollama`,
`adapters/anthropic`, and `adapters/gemini`. Build one for the machine where it
will run, then create and install a sealed package:

```sh
go build -o /tmp/ext-openai ./adapters/openai
go build -o /tmp/ext-ctn ./src/ctn/cmd/ctn
/tmp/ext-ctn inference package openai 1.0.0 /tmp/ext-openai /tmp/openai-package
/tmp/ext-ctn inference install /tmp/openai-package
/tmp/ext-ctn inference providers
```

`package` refuses to replace an existing output directory. `install` refuses
to replace an installed package and prints its package digest. The default
store is `<EXT_HOME>/commands` (or the shared Ext config home); set
`EXT_INT_PACKAGE_ROOT` to use another store. The previous
`EXT_INFERENCE_PACKAGE_ROOT` override remains a fallback. Installation alone grants no
execution authority. After reviewing the package, record its exact digest:

```sh
/tmp/ext-ctn inference trust openai
export EXT_ADAPTER_VALUE_endpoint=https://api.openai.com/v1
export EXT_ADAPTER_VALUE_api_key=<provider-key>
/tmp/ext-ctn inference capabilities --provider openai
```

`ctn inference untrust openai` revokes the stored grant. Existing deployments
can still use `EXT_INFERENCE_TRUST_<ID>` with uppercase ID and `/`, `.`, and `-`
replaced by `_`; its value must equal the current package digest. CTN rechecks
the package, trust record, and executable digest immediately before launching
the guest. The provider endpoint is explicit;
none of the guests silently selects a remote service. Ollama accepts an empty
API key and an OpenAI-compatible endpoint such as
`http://127.0.0.1:11434/v1`. A changed package needs a newly reviewed digest.

The guest interfaces are portable. A non-Go guest can serve the same
`inference.provider/v1` operations and Ext JSON-line process transport; the Go
package helper is only a convenience for Go-built executables. The package
manifest, not the implementation language, controls discovery.

## Current adapters

| Guest | Implemented operations |
| --- | --- |
| OpenAI-compatible, Ollama | generation, streaming, embeddings, model listing, tool proposals through chat completions |
| Anthropic | generation, inline images, tool proposals and results, JSON Schema output through Messages |
| Gemini | generation, inline images, function proposals and results, JSON Schema output through `generateContent` |

Unsupported operations and media types return a typed `ErrUnsupported`
response. Anthropic and Gemini mappings are covered by local wire tests;
provider credentials are required for live API verification.

## Native model backends

`res/int/model/ios` contains a CoreML backend for verified `.mlmodel` artifacts and
float32 multi-array tensors. Android has an optional TensorFlow Lite backend
for verified `.tflite` artifacts; the host app supplies its interpreter
runtime. Windows and Linux have an optional bounded ONNX Runtime CPU backend
with self-contained CTN guests. The Android NNAPI class remains an unavailable
stub. CTN does not select a native provider automatically.
