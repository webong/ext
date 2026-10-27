# ml

`github.com/webong/ext/res/ml` is the reusable local on-device model layer. It
owns portable contracts for model manifests, verified artifact installation,
and bounded inference; native model backends (CoreML on Apple platforms, NNAPI
on Android, ONNX Runtime/DirectML on Windows/Linux) stay beside these
contracts as platform implementations or in product adapters.

The boundary is local mechanism, not model policy:

- callers select the model root and the model bundle to load;
- artifact installation stages, verifies SHA-256 digests, and atomically
  publishes only after all checks pass;
- inference runs through a caller-selected provider against a loaded model;
- tensor buffers are bounded and caller-owned;
- no model download, no provider selection, no authorization, no approval UI,
  no remote endpoint, and no product protocol live here.

| Package | What it is |
| --- | --- |
| `ml` (module root) | Model manifest types, verified artifact store, model inspection, and bounded inference interfaces |
| `ios/` (`ExtML`) | Apple CoreML-backed model provider shipped through `pkg/ext-ios` |
| `android/` | Android NNAPI/TensorFlow Lite model provider compiled into the `pkg/ext-android` AAR |

Product-specific model registries, provider contracts, event schemas, inference
schemas, and model download belong to the consumer (for example Cymonkey's
content provider adapters). `res/ml` owns none of them.
