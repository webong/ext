# Intelligence resources

`src/ctn/contract` retains its published Go intelligence contracts. Native model
libraries live in each platform package's `int/model`; provider guests live in
`xallet.app/tab/coreml` and `xallet.app/tab/onnxruntime`.
Applications choose providers, models,
credentials, roots, and authorization policy.

| Package | Scope |
| --- | --- |
| `model` | Go model contracts and verified bundles; Core ML, Android and .NET backends are in `pkg/ext-darwin`, `pkg/ext-android` and `pkg/ext-windows` under `int/model` |
| `inference` | Generation, embeddings, transcription, speech, and provider contracts |
| `perception` | Multimodal findings and image observations |
| `realtime` | Duplex model session and event contracts |

The wire contracts remain versioned independently of Go import paths. Go
hosts and guests use [CTN’s public `model.tensor/v1` bridge](../../src/ctn/model) for local
tensor engines. Other languages can implement the same JSON contract through
the Ext plugin engine.

The previous `res/ml` and `res/inference` Go modules are combined into this
module. Their packages are now `src/ctn/contract/model` and `src/ctn/contract/inference`; the
existing `model.tensor/v1`, `inference.provider/v1`, and `content.model/v1`
wire names remain stable. CTN owns its verified file catalog in
`src/ctn/internal/content`; it is private product code and does not require an
intelligence provider.

Product-specific provider wire mappings belong in the provider's adapter,
outside ext (see [adapter repositories](../../docs/adapter-repositories.md)). The plugin
engine owns package integrity and trust mechanics. CTN selects installed
providers, explicit models, roots, credentials, and user-facing workflows.
