package io.github.webong.ext.plugin;

public record Operation(String name, String surface) {
    public Operation(String name) {
        this(name, null);
    }
}
