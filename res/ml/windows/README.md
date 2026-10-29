# Windows ML

`res/ml/windows/SystemMl.cs` contains a bounded Windows ONNX inference backend
owned by `res/ml`. `Ext.ML.Windows.csproj` supplies the optional
`Microsoft.ML.OnnxRuntime` runtime. The initial backend uses CPU execution.

`SystemMl` accepts an explicit model file path and policy. It validates float32
input shapes and byte bounds, runs all model outputs, and releases native
session resources. Output shapes must be fixed so their size can be checked
before native execution. It does not download models, select devices, authorize
access, or expose a remote protocol.

```csharp
using Webong.Ext.ML.Windows;

var policy = new MlPolicy("model.onnx", DeviceKind: "cpu");
using var ml = new SystemMl(policy.ModelFile, policy);
using var session = ml.LoadSession();
var outputs = ml.Infer(new[] { MlTensor.FromFloat32(new[] { 1, 3, 224, 224 }, input) });
```
