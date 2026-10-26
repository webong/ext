package io.github.webong.ext.clipboard;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

public final class ClipboardItem {
    public final List<ClipboardRepresentation> representations;
    public ClipboardItem(List<ClipboardRepresentation> representations) {
        this.representations = Collections.unmodifiableList(new ArrayList<>(representations));
    }
}
