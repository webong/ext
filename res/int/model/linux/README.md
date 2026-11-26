# Linux ML

Linux can use the optional ONNX Runtime CPU provider in
[`adapters/onnxruntime`](../../../../adapters/onnxruntime). That adapter owns
the Linux and Windows `model.tensor/v1` guests and their standalone builds.

The Go default provider reports `ErrUnavailable`; applications select an
installed, trusted provider explicitly. Portable model contracts remain in
`res/int/model`.
