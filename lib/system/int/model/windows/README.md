# Windows ML

`lib/system/int/model/windows/SystemMl.cs` contains a bounded Windows/Linux ONNX inference backend
exposed through the `onnxruntime` adapter. `Ext.ML.Windows.csproj` supplies the optional
`Microsoft.ML.OnnxRuntime` runtime. The initial backend uses CPU execution.

`SystemMl` accepts an explicit model file path and policy. It validates float32
input shapes and byte bounds, runs all model outputs, and releases native
session resources. Output shapes must be fixed so their size can be checked
before native execution. It does not download models, select devices, authorize
access, or expose a remote protocol.

The provider guests live in [`xallet.app/tab/onnxruntime`](https://github.com/xallet/tab/tree/main/onnxruntime).
They consume the published library, serve `model.tensor/v1` over Ext JSON lines,
and verify model manifests and artifact hashes before inference. See the adapter
README for standalone builds and installation.

```csharp
using Webong.Ext.ML.Windows;

var policy = new MlPolicy("model.onnx", DeviceKind: "cpu");
using var ml = new SystemMl(policy.ModelFile, policy);
using var session = ml.LoadSession();
var outputs = ml.Infer(new[] { MlTensor.FromFloat32(new[] { 1, 3, 224, 224 }, input) });
```

## Install

`Ext.ML.Windows` is released as a NuGet package of that name, at the version of the `ext-windows-vX.Y.Z` tag
(see [module versioning](https://github.com/webong/ext/blob/main/docs/module-versioning.md)). The types are in the namespace `Webong.Ext.ML.Windows`.

```powershell
dotnet add package Ext.ML.Windows
```

The package is attached to the GitHub release of its tag with a checksum file and a provenance
attestation, and is pushed to a NuGet feed when the repository is configured with one.
