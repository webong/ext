package io.github.webong.ext.ml;

import java.io.InputStream;

/**
 * A bounded, readable source for one model artifact. The primary installation
 * API accepts streaming sources so that large artifacts can be hashed and
 * staged without ever being held entirely in memory.
 */
public interface ArtifactSource {
    /** Opens a readable stream for the artifact. The caller must close the returned stream. */
    InputStream open() throws MlException;

    /** Returns the declared byte size of the artifact, or -1 if unknown. */
    long size();
}
