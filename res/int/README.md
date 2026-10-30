# Intelligence resources

`res/int` is the reusable intelligence module. Its packages define portable
contracts and bounded mechanisms; applications choose providers, models,
credentials, roots, and authorization policy.

| Package | Scope |
| --- | --- |
| `model` | Verified local model bundles, tensors, and native backends |
| `inference` | Generation, embeddings, transcription, speech, and provider contracts |
| `perception` | Multimodal findings and image observations |
| `realtime` | Duplex model session and event contracts |

The wire contracts remain versioned independently of Go import paths. Go
hosts and guests use bridges in `pkg/ext-go`; other languages can implement the
same JSON contracts through the Ext plugin engine.

The previous `res/ml` and `res/inference` Go modules are combined into this
module. Their packages are now `res/int/model` and `res/int/inference`; the
existing `model.tensor/v1`, `inference.provider/v1`, and `content.model/v1`
wire names remain stable. CTN owns its verified file catalog in
`src/ctn/internal/content`; it is private product code and does not require an
intelligence provider.

Product-specific provider wire mappings belong in `adapters/`. The plugin
engine owns package integrity and trust mechanics. CTN selects installed
providers, explicit models, roots, credentials, and user-facing workflows.
