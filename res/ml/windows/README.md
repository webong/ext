# Windows ML

`res/ml/windows/SystemMl.cs` contains a bounded Windows/Linux ONNX inference backend
owned by `res/ml`. `Ext.ML.Windows.csproj` supplies the optional
`Microsoft.ML.OnnxRuntime` runtime. The initial backend uses CPU execution.

`SystemMl` accepts an explicit model file path and policy. It validates float32
input shapes and byte bounds, runs all model outputs, and releases native
session resources. Output shapes must be fixed so their size can be checked
before native execution. It does not download models, select devices, authorize
access, or expose a remote protocol.

`Guest/Ext.ML.Windows.Guest.csproj` builds a self-contained Windows executable
that serves `model.tensor/v1` over Ext JSON lines. Publish with
`dotnet publish -c Release -r win-x64 --self-contained true -p:PublishSingleFile=true -p:IncludeNativeLibrariesForSelfExtract=true`.
Package the resulting executable as `onnxruntime` version `1.0.0`, then install
and trust that exact package. The guest verifies the model manifest and artifact
hashes on each call. The Linux guest in `../linux/Guest` uses the same backend.
The Windows host must provide `%TEMP%` or `DOTNET_BUNDLE_EXTRACT_BASE_DIR`
for the self-contained .NET executable to extract its native libraries; CTN
forwards the caller's environment to this guest.

```csharp
using Webong.Ext.ML.Windows;

var policy = new MlPolicy("model.onnx", DeviceKind: "cpu");
using var ml = new SystemMl(policy.ModelFile, policy);
using var session = ml.LoadSession();
var outputs = ml.Infer(new[] { MlTensor.FromFloat32(new[] { 1, 3, 224, 224 }, input) });
```
