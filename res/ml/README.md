# ml

`github.com/webong/ext/res/ml` is the reusable local on-device model layer. It
owns portable contracts for model manifests, verified artifact installation,
and bounded inference; native model backends (CoreML on Apple platforms,
optional TensorFlow Lite on Android, and ONNX Runtime CPU on Windows) stay beside these
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
| `ios/` (`ExtML`) | Apple CoreML-backed model provider shipped through `pkg/ext-ios` (macOS and iOS) |
| `android/` | Optional TensorFlow Lite float32 provider compiled into the `pkg/ext-android` AAR; NNAPI is an unavailable stub |
| `windows/` | Optional ONNX Runtime CPU backend in `res/ml/windows` |
| `linux/` | ONNX Runtime stub; native binding belongs in a product adapter |

The shared provider contract and inference schemas live in `res/inference`.
Product-specific model registries, download policy, and content adapters belong
to each consumer. `res/ml` owns none of them.

`pkg/ext-go/ml` exposes a language-neutral `model.tensor/v1` process contract
for a caller-selected `ml.Provider`. CTN installs and trusts those provider
packages before `ctn model run` calls them. The native Swift, Java, and C#
backends need guest executables that serve this contract to run from CTN.
