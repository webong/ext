# Android ML library

The portable Java model contracts live under `res/ml/android/src/main/java/io/github/webong/ext/ml/`
and compile into the `pkg/ext-android` AAR as `io.github.webong.ext.ml`.

The Android-native binding lives under `src/android/java/io/github/webong/ext/ml/android/`
and is compiled into the same AAR. `TfliteProvider` runs verified `.tflite`
artifacts with float32 tensors when the host app supplies the optional
`org.tensorflow.lite.Interpreter` runtime. It does not bundle or select that
runtime. It checks the artifact digest on load and bounds input and output
tensor sizes. The NNAPI provider remains an unavailable stub.

The library owns manifest parsing, verified artifact installation, model
inspection, and bounded inference. It never downloads a model, selects a
provider, or authorizes access. `pkg/ext-android` includes
`io.github.webong.ext.plugin.ModelTensorGuest`, which binds a caller-selected
`MlProvider` to the shared `model.tensor/v1` guest contract. Applications supply
the plugin identity, authorization predicate, and optional JSON-line process
entry point.
