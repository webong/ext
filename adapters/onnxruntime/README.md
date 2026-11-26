# ONNX Runtime provider

This adapter owns the Windows and Linux `model.tensor/v1` guests. They expose
the `onnxruntime` provider over Ext JSON lines, verify local model bundles and
artifact hashes, and run bounded CPU inference. CTN chooses the provider and
model; installation does not grant trust.

The projects pin `Ext.ML.Windows` 0.1.0. `build.py` downloads that published
NuGet package from `ext-windows-v0.1.0`, verifies its pinned SHA-256 and restores
its dependencies from NuGet. No project references the Ext workspace. Copy
this entire adapter directory elsewhere to build it independently with Python
3 and the .NET 8 SDK:

```sh
python3 build.py linux-x64 --output out/linux-x64
```

Use `win-x64` for Windows. The outputs are `Ext.ML.Linux.Guest` and
`Ext.ML.Windows.Guest.exe`, respectively. Native CI builds isolated copies and
tests each guest through CTN. The guest protocol and provider identity remain
unchanged.

Seal the executable using Ext and this adapter’s descriptor:

```sh
ext package pack --descriptor descriptor.json --protocol model.tensor/v1 \
  --os linux --arch amd64 --binary out/linux-x64/Ext.ML.Linux.Guest \
  --output out/onnxruntime.linux-amd64
ext package install out/onnxruntime.linux-amd64
ext package trust onnxruntime
```

For Windows, use `--os windows` and the Windows executable. Publish sealed
archives through an Ext package source; see
[registry releases](../../docs/source-management.md#publish-an-adapter-release).

Self-contained .NET executables extract native libraries before the guest
starts. The host must provide a private writable extraction location through
`DOTNET_BUNDLE_EXTRACT_BASE_DIR`, or the platform default (`HOME` on Linux,
`TEMP` on Windows). CTN forwards the caller's environment.
