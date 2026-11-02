package io.github.webong.ext.clipboard;

public final class ClipboardRepresentation {
    public final String format;
    public final byte[] data;
    public ClipboardRepresentation(String format, byte[] data) {
        this.format = format;
        this.data = data.clone();
    }
}
