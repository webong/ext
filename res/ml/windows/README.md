# Windows ML

`res/ml/windows/SystemMl.cs` contains reusable Windows-native ML primitives owned
by `res/ml`. The first surface is bounded Windows ML (WinML) inference through
the `Windows.AI.MachineLearning` API.

`SystemMl` accepts an explicit model file path and policy; it does not download
models, select devices, authorize access, or expose a remote protocol. The
WinML interop to `Windows.AI.MachineLearning` is declared here; the actual P/Invoke
binding is implemented by the caller's host or a product adapter.

```csharp
using Webong.Ext.System.Windows;

var policy = new MlPolicy("model.onnx", deviceKind: "cpu");
using var ml = new SystemMl(policy.ModelFile, policy);
using var session = ml.LoadSession();
var outputs = ml.Infer(new[] { MlTensor.FromFloat32(new[] { 1, 3, 224, 224 }, input) });
```
