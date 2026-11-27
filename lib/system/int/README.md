# Intelligence libraries

`lib/system/int` owns native intelligence mechanisms. `model/ios` supplies
Core ML, `model/android` supplies Android model backends, and `model/windows`
supplies the .NET ONNX backend. Provider-specific guest entrypoints are
separate: Core ML's is in `xallet.app/tab/coreml`. The existing Go contracts stay at
their published paths in `res/int/model`.
