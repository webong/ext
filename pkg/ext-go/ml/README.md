# Tensor model plugin bridge

`pkg/ext-go/ml` binds `res/ml` to the language-neutral Ext plugin contract
`model.tensor/v1`. A guest serves the `infer` operation over the Ext JSON-line
transport. Its request contains an absolute model-store root, a
`provider/name@version` reference, and 1 to 64 float32 tensors. The response
contains 1 to 64 float32 tensors. Tensor bytes are JSON base64; one call allows
at most 16 MiB of raw input or output tensor data. Larger tensors need a future
stream or shared-memory transport.
The JSON request and response fixtures are in `testdata/v1`.

Go guests can register an `ml.Provider` with `RegisterProvider`, then call
`ServeCurrentExecutable`. The guest reopens and verifies the model bundle on
each call. Non-Go guests can implement the same descriptor and JSON payloads.
The host must choose a sealed package and record an exact scoped trust digest.
The guest's ID and version must match the values used when packaging it.

CTN's flow is:

```sh
ctn model provider package onnxruntime 1.0.0 /path/to/provider /path/to/package
ctn model provider install /path/to/package
ctn model provider list
ctn model provider trust onnxruntime
ctn model run --provider onnxruntime /absolute/model-store \
  onnxruntime/model@v1 /path/to/tensors.json
ctn model provider untrust onnxruntime
```

`tensors.json` is `{"tensors":[{"shape":[1,2],"format":"float32","data":"...base64..."}]}`.
`model run` returns the same shape with output tensors, or writes it to a new
file with `--output FILE`. A test guest exercises packaging, trust, process
transport, model verification, and tensor output end to end. Platform SDKs
provide CoreML, TensorFlow Lite, and ONNX Runtime implementations. On macOS,
`res/ml/ios` also builds `ext-coreml-guest`, a Swift guest that connects the
CoreML backend to this contract. Package it as ID `coreml`, version `1.0.0`.
The Android and Windows native backends still need process guests to run under
CTN.
