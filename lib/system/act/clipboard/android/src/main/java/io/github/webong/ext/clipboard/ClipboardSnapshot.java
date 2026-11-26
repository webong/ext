package io.github.webong.ext.clipboard;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

public final class ClipboardSnapshot {
    public final String generation;
    public final List<ClipboardItem> items;
    public ClipboardSnapshot(String generation, List<ClipboardItem> items) {
        this.generation = generation;
        this.items = Collections.unmodifiableList(new ArrayList<>(items));
    }
}
