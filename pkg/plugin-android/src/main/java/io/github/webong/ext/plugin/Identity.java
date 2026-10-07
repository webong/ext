package io.github.webong.ext.plugin;

public record Identity(String id, String revision, String version) {
    public Identity(String id, String revision) {
        this(id, revision, null);
    }
}
