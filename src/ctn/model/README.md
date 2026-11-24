# CTN tensor model bridge compatibility

The canonical Go bridge and wire fixtures are in
[`res/int/model/plugin`](../../../res/int/model/plugin). This package preserves
the published CTN Go import path for existing users. New hosts and guests use
the shared Ext module directly.

CTN still operates on installed and explicitly trusted provider packages:

```sh
ext ctn model provider package onnxruntime 1.0.0 /path/to/provider /path/to/package
ext ctn model provider install /path/to/package
ext ctn model provider list
ext ctn model provider trust onnxruntime
ext ctn model run --provider onnxruntime /absolute/model-store \
  onnxruntime/model@v1 /path/to/tensors.json
ext ctn model provider untrust onnxruntime
```

`tensors.json` contains a `tensors` array with the shape, format, and base64
data for each tensor. `model run` returns the same shape or writes the
response to a new file with `--output FILE`.
