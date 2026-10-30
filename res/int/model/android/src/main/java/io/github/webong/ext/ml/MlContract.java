package io.github.webong.ext.ml;

public final class MlContract {
    public static final String MANIFEST_API_VERSION = "content.model/v1";
    public static final int MAX_MANIFEST_BYTES = 64 * 1024;
    public static final int MAX_ARTIFACTS = 64;
    public static final long MAX_ARTIFACT_BYTES = 64L * 1024 * 1024;
    public static final int MAX_NAME_LENGTH = 128;
    public static final int MAX_CAPABILITIES = 32;

    private MlContract() {}
}
