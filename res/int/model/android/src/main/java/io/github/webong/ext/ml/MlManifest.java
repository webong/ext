package io.github.webong.ext.ml;

import java.util.List;

public final class MlManifest {
    public final String apiVersion;
    public final String name;
    public final String version;
    public final String provider;
    public final List<String> capabilities;
    public final List<MlArtifact> artifacts;

    public MlManifest(String apiVersion, String name, String version, String provider,
            List<String> capabilities, List<MlArtifact> artifacts) {
        this.apiVersion = apiVersion;
        this.name = name;
        this.version = version;
        this.provider = provider;
        this.capabilities = capabilities == null ? List.of() : List.copyOf(capabilities);
        this.artifacts = artifacts == null ? List.of() : List.copyOf(artifacts);
    }

    public void validate() throws MlException {
        if (!MlContract.MANIFEST_API_VERSION.equals(apiVersion)) {
            throw new MlException(MlException.Kind.INVALID, "manifest apiVersion must be " + MlContract.MANIFEST_API_VERSION);
        }
        MlValidation.isValidName(name, "model name");
        MlValidation.isValidVersion(version);
        MlValidation.isValidName(provider, "provider");
        if (capabilities.size() > MlContract.MAX_CAPABILITIES) {
            throw new MlException(MlException.Kind.INVALID, "too many capabilities");
        }
        java.util.Set<String> seen = new java.util.HashSet<String>();
        for (String cap : capabilities) {
            MlValidation.isValidName(cap, "capability");
            if (!seen.add(cap)) {
                throw new MlException(MlException.Kind.INVALID, "duplicate capability");
            }
        }
        if (artifacts.size() < 1 || artifacts.size() > MlContract.MAX_ARTIFACTS) {
            throw new MlException(MlException.Kind.INVALID, "manifest needs 1-" + MlContract.MAX_ARTIFACTS + " artifacts");
        }
        java.util.Set<String> seenFiles = new java.util.HashSet<String>();
        java.util.Set<String> seenRoles = new java.util.HashSet<String>();
        for (MlArtifact artifact : artifacts) {
            artifact.validate();
            if (!seenFiles.add(artifact.file)) {
                throw new MlException(MlException.Kind.INVALID, "duplicate artifact file");
            }
            if (!seenRoles.add(artifact.role)) {
                throw new MlException(MlException.Kind.INVALID, "duplicate artifact role");
            }
        }
    }
}
