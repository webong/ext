package io.github.webong.ext.ml;

public final class MlInstalledModel {
    public final MlManifest manifest;
    public final String directory;

    public MlInstalledModel(MlManifest manifest, String directory) {
        this.manifest = manifest;
        this.directory = directory;
    }
}
