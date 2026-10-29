# Linux ML

Linux has an optional ONNX Runtime CPU backend through the .NET implementation
in `res/ml/windows/SystemMl.cs`. The project in `Guest/Ext.ML.Linux.Guest.csproj`
links the same `model.tensor/v1` process guest. Publish it with
`dotnet publish -c Release -r linux-x64 --self-contained true -p:PublishSingleFile=true -p:IncludeNativeLibrariesForSelfExtract=true`,
then package, install, and trust the `Ext.ML.Linux.Guest` executable as
`onnxruntime` version `1.0.0`.

The Go `res/ml/ml_linux.go` default provider still reports `ErrUnavailable`;
applications opt into the ONNX backend explicitly through the guest package.
The self-contained .NET executable extracts native libraries before its `run`
entry point starts. A host must provide `HOME` or
`DOTNET_BUNDLE_EXTRACT_BASE_DIR` pointing to a private writable directory;
CTN forwards the caller's environment to this guest.

The portable `res/ml` contracts (manifest, store, tensor, provider/session
interfaces) are fully available on Linux through the Go module.
