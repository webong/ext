# CTN tensor model plugins

This public CTN library binds the reusable model types in `res/int/model` to
`model.tensor/v1`. CTN, Cymonkey, and other Ext hosts can each open a sealed
provider package directly. The application selects the package and model
store, reviews the package digest, and authorizes calls. The application opens
the provider through Ext using its own host session.

## Contract

A provider exposes `model.tensor.infer` through Ext. The request contains an
absolute model-store root, a `provider/name@version` reference, and 1 to 64
float32 tensors. The response contains 1 to 64 float32 tensors. Tensor bytes
use JSON base64 and each direction is limited to 16 MiB of raw tensor data.
See the [request and response fixtures](testdata/v1/). Host and guest
implementations can use any language that supports Ext's plugin protocol.

## Use from an Ext host

1. Build a provider guest, then seal its executable with `PackageExecutable`
   and install the package with the Ext package store. The executable's entry
   takes the `run` argument. Ext provides native guests for
   [Core ML](../../../lib/system/int/model/ios/README.md) on macOS and [ONNX Runtime](../../../lib/system/int/model/windows/README.md)
   on Windows and [Linux](../../../res/int/model/linux/README.md).
2. Use `store.ResolveEntrypoint` with the package ID, `ContractName`,
   `ContractVersion`, `Protocol`, and the consumer's reviewed package digest.
   Open that executable with the Ext host, pass `run`, and supply the host's
   authorization and any required runtime environment. Recheck the reviewed
   digest before launch.
3. Call `NewProcessClient(process).Infer(ctx, request)` and close the process
   when finished. Use `NewClient` for a host that already has a typed Ext
   session. Guests written in Go can use `RegisterProvider` or
   `ServeCurrentExecutable`.

Workflows such as image resizing, decoding YOLO output into detections, and
deciding which provider to use sit above this tensor contract. The existing
CTN CLI is another consumer of the same capability.

## Use from CTN

CTN operates on installed and explicitly trusted provider packages:

```sh
ext ctn model provider package onnxruntime 1.0.0 /path/to/provider /path/to/package
ext ctn model provider install /path/to/package
ext ctn model provider list
ext ctn model provider trust onnxruntime
ext ctn model run --provider onnxruntime /absolute/model-store \
  onnxruntime/model@v1 /path/to/tensors.json
ext ctn model provider untrust onnxruntime
```

`tensors.json` contains a `tensors` array with the shape, format, and base64
data for each tensor. `model run` returns the same shape or writes the
response to a new file with `--output FILE`.
