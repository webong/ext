package io.github.webong.ext.clipboard;

public final class ClipboardReceipt {
    public final String generation, dispatch;
    public ClipboardReceipt(String generation, String dispatch) {
        this.generation = generation;
        this.dispatch = dispatch;
    }
}
