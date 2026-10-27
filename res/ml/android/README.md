# Android ML library

The portable Java model contracts live under `res/ml/android/src/main/java/io/github/webong/ext/ml/`
and compile into the `pkg/ext-android` AAR as `io.github.webong.ext.ml`.

The Android-native binding lives under `src/android/java/io/github/webong/ext/ml/android/`
and is compiled into the same AAR. It currently contains an NNAPI provider stub;
native model backends (NNAPI, TensorFlow Lite) belong here as platform
implementations of the portable `MlProvider` contract.

The library owns manifest parsing, verified artifact installation, model
inspection, and bounded inference interfaces only. It never downloads a model,
selects a provider, runs inference, authorizes access, or exposes a remote
protocol.
