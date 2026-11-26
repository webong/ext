# Intelligence libraries

`lib/system/int` owns native intelligence mechanisms. `model/ios` supplies
Core ML, `model/android` supplies Android model backends, and `model/windows`
supplies the .NET ONNX backend. Their Ext guest entrypoints remain under
`res/int/model`; the existing Go contracts stay at their published paths.
