# ml

`github.com/webong/ext/res/int/model` is the local model package in the reusable
`github.com/webong/ext/res/int` module. It
owns portable contracts for model manifests, verified artifact installation,
and bounded inference; native model backends (CoreML on Apple platforms,
optional TensorFlow Lite on Android, and ONNX Runtime CPU on Windows/Linux) stay beside these
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
| `ios/` (`ExtML`) | Apple CoreML-backed model provider shipped through `pkg/ext-darwin` (macOS and iOS) |
| `android/` | Optional TensorFlow Lite float32 provider compiled into the `pkg/ext-android` AAR; NNAPI is an unavailable stub |
| `windows/` | Optional ONNX Runtime CPU backend and `model.tensor/v1` guest, usable on Windows and Linux |
| `linux/` | Linux guest build of the shared ONNX Runtime CPU backend |

The shared provider contract and inference schemas live in `res/int/inference`.
Product-specific model registries, download policy, and content adapters belong
to each consumer. `res/int/model` owns none of them.

[CTN’s public model bridge](../../../src/ctn/model) exposes `model.tensor/v1`
for a caller-selected `ml.Provider`. CTN and other Ext hosts each select
and trust provider packages directly. The Swift CoreML and .NET ONNX guests
serve the same contract. The Android SDK includes a
`ModelTensorGuest` binding for app-hosted TensorFlow Lite with caller-supplied
identity and authorization.
