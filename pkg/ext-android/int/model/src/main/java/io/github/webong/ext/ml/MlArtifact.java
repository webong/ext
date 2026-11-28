package io.github.webong.ext.ml;

public final class MlArtifact {
    public final String role;
    public final String file;
    public final String sha256;

    public MlArtifact(String role, String file, String sha256) {
        this.role = role;
        this.file = file;
        this.sha256 = sha256;
    }

    public void validate() throws MlException {
        if (!MlValidation.isValidName(role, "artifact role")) {
            throw new MlException(MlException.Kind.INVALID, "invalid artifact role");
        }
        if (!MlValidation.isValidName(file, "artifact file")) {
            throw new MlException(MlException.Kind.INVALID, "invalid artifact file");
        }
        if (!MlValidation.isHexSha256(sha256)) {
            throw new MlException(MlException.Kind.INVALID, "invalid sha256 digest");
        }
    }
}
