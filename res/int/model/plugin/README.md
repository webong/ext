# Ext tensor model plugins

This Go module binds the shared model types in `res/int/model` to
`model.tensor/v1`. CTN, Cymonkey, and other Ext hosts can each open a sealed
provider package directly. The application selects the package and model
store, reviews the package digest, and authorizes calls.

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
   [Core ML](../ios/README.md) on macOS and [ONNX Runtime](../windows/README.md)
   on Windows and [Linux](../linux/README.md).
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
