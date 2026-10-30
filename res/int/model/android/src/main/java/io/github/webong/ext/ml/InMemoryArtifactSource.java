package io.github.webong.ext.ml;

import java.io.InputStream;

/**
 * An in-memory artifact source wrapping a byte array. A convenience wrapper
 * for small artifacts; large artifacts should use a streaming source that
 * reads from disk or network.
 */
public final class InMemoryArtifactSource implements ArtifactSource {
    private final byte[] data;

    public InMemoryArtifactSource(byte[] data) {
        this.data = data == null ? new byte[0] : data.clone();
    }

    @Override
    public InputStream open() throws MlException {
        return new java.io.ByteArrayInputStream(data);
    }

    @Override
    public long size() {
        return data.length;
    }
}
