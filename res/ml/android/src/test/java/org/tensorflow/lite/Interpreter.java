package org.tensorflow.lite;

import java.nio.ByteBuffer;
import java.nio.ByteOrder;
import java.util.Map;

/** Test stand-in for the optional TensorFlow Lite Java runtime. */
public final class Interpreter implements AutoCloseable {
    private boolean closed;
    public Interpreter(ByteBuffer model) {
        if (model.remaining() == 0) throw new IllegalArgumentException("empty model");
    }
    public int getInputTensorCount() { return 1; }
    public int getOutputTensorCount() { return 1; }
    public Tensor getInputTensor(int index) { return new Tensor(index); }
    public Tensor getOutputTensor(int index) { return new Tensor(index); }
    public void runForMultipleInputsOutputs(Object[] inputs, Map<Integer, Object> outputs) {
        if (closed) throw new IllegalStateException("closed");
        ByteBuffer in = ((ByteBuffer) inputs[0]).order(ByteOrder.nativeOrder());
        ByteBuffer out = ((ByteBuffer) outputs.get(0)).order(ByteOrder.nativeOrder());
        out.putFloat(0, in.getFloat(0) * 2);
        out.putFloat(4, in.getFloat(4) * 2);
    }
    @Override public void close() { closed = true; }
    public static final class Tensor {
        Tensor(int index) { if (index != 0) throw new IllegalArgumentException("index"); }
        public int[] shape() { return new int[]{1, 2}; }
        public DataType dataType() { return DataType.FLOAT32; }
    }
    public enum DataType { FLOAT32 }
}
