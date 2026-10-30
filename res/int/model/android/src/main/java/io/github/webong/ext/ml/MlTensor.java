package io.github.webong.ext.ml;

public final class MlTensor {
    public final int[] shape;
    public final byte[] data;
    public final String format;

    public MlTensor(int[] shape, byte[] data, String format) {
        this.shape = shape == null ? new int[0] : shape.clone();
        this.data = data == null ? new byte[0] : data.clone();
        this.format = format;
    }

    public int elementCount() {
        int count = 1;
        for (int dim : shape) count *= dim;
        return count;
    }

    public float[] float32Values() {
        if (data.length % 4 != 0) return new float[0];
        float[] values = new float[data.length / 4];
        for (int i = 0; i < values.length; i++) {
            int b0 = data[i * 4] & 0xFF;
            int b1 = data[i * 4 + 1] & 0xFF;
            int b2 = data[i * 4 + 2] & 0xFF;
            int b3 = data[i * 4 + 3] & 0xFF;
            int bits = b0 | (b1 << 8) | (b2 << 16) | (b3 << 24);
            values[i] = Float.intBitsToFloat(bits);
        }
        return values;
    }

    public static MlTensor fromFloat32(int[] shape, float[] values) throws MlException {
        byte[] data = new byte[values.length * 4];
        for (int i = 0; i < values.length; i++) {
            int bits = Float.floatToIntBits(values[i]);
            data[i * 4] = (byte) (bits & 0xFF);
            data[i * 4 + 1] = (byte) ((bits >> 8) & 0xFF);
            data[i * 4 + 2] = (byte) ((bits >> 16) & 0xFF);
            data[i * 4 + 3] = (byte) ((bits >> 24) & 0xFF);
        }
        MlTensor tensor = new MlTensor(shape, data, "float32");
        tensor.validate();
        return tensor;
    }

    public void validate() throws MlException {
        if (!"float32".equals(format)) {
            throw new MlException(MlException.Kind.INVALID, "tensor format must be float32");
        }
        if (shape.length < 1 || shape.length > 8) {
            throw new MlException(MlException.Kind.INVALID, "tensor must have 1-8 dimensions");
        }
        long elements = 1;
        for (int dim : shape) {
            if (dim < 1 || dim > (1 << 30)) {
                throw new MlException(MlException.Kind.INVALID, "tensor dimension out of range");
            }
            if (elements > (long) (1 << 30) / dim) {
                throw new MlException(MlException.Kind.INVALID, "tensor element count overflows");
            }
            elements *= dim;
        }
        if (data.length != elements * 4) {
            throw new MlException(MlException.Kind.INVALID, "tensor data length mismatch");
        }
    }
}
