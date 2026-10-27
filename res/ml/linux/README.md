# Linux ML

Linux native ML inference (ONNX Runtime via cgo, or native libtorch) is not
implemented in `res/ml/ml_linux.go`; it reports `ErrUnavailable`. A native
Linux ML backend belongs in a product adapter (for example
`adapters/onnxruntime`), not in `res/ml`.

The portable `res/ml` contracts (manifest, store, tensor, provider/session
interfaces) are fully available on Linux through the Go module.
