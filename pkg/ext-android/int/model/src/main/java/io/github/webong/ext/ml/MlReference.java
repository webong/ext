package io.github.webong.ext.ml;

public final class MlReference {
    public final String provider;
    public final String name;
    public final String version;

    public MlReference(String provider, String name, String version) {
        this.provider = provider;
        this.name = name;
        this.version = version;
    }

    @Override
    public String toString() {
        return provider + "/" + name + "@" + version;
    }
}
