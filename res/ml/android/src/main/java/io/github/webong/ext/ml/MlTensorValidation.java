package io.github.webong.ext.ml;

public final class MlTensorValidation {
    private MlTensorValidation() {}

    public static void validate(MlTensor tensor) throws MlException {
        if (!"float32".equals(tensor.format)) {
            throw new MlException(MlException.Kind.INVALID, "tensor format must be float32");
        }
        if (tensor.shape.length < 1 || tensor.shape.length > 8) {
            throw new MlException(MlException.Kind.INVALID, "tensor must have 1-8 dimensions");
        }
        long elements = 1;
        for (int dim : tensor.shape) {
            if (dim < 1 || dim > (1 << 30)) {
                throw new MlException(MlException.Kind.INVALID, "tensor dimension out of range");
            }
            if (elements > (long) (1 << 30) / dim) {
                throw new MlException(MlException.Kind.INVALID, "tensor element count overflows");
            }
            elements *= dim;
        }
        if (tensor.data.length != elements * 4) {
            throw new MlException(MlException.Kind.INVALID, "tensor data length mismatch");
        }
    }
}
