package io.github.webong.ext.clipboard.android;

import android.content.ClipData;
import android.content.ClipDescription;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.os.Build;
import android.os.Looper;
import io.github.webong.ext.clipboard.ClipboardException;
import io.github.webong.ext.clipboard.ClipboardItem;
import io.github.webong.ext.clipboard.ClipboardPolicy;
import io.github.webong.ext.clipboard.ClipboardReceipt;
import io.github.webong.ext.clipboard.ClipboardRepresentation;
import io.github.webong.ext.clipboard.ClipboardSnapshot;
import io.github.webong.ext.clipboard.ClipboardValidation;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.UUID;

/** Caller-owned, UI-thread binding to Android's app-visible system clipboard. */
public final class SystemClipboard implements AutoCloseable {
    public static final String TEXT = "text/plain";
    public static final String HTML = "text/html";
    public static final String URI = "text/uri-list";
    public static final String INTENT = "application/vnd.android.intent";

    private final ClipboardManager manager;
    private final ClipboardPolicy policy;
    private final String producer = UUID.randomUUID().toString();
    private long change;
    private boolean pendingOwnChange, closed;
    private final ClipboardManager.OnPrimaryClipChangedListener listener = () -> {
        if (pendingOwnChange) pendingOwnChange = false;
        else change++;
    };

    public SystemClipboard(Context context, ClipboardPolicy policy) {
        requireUI();
        this.policy = policy;
        manager = (ClipboardManager) context.getApplicationContext().getSystemService(Context.CLIPBOARD_SERVICE);
        if (manager == null) throw new IllegalStateException("clipboard service is unavailable");
        manager.addPrimaryClipChangedListener(listener);
    }

    /** Returns mutation metadata without reading clipboard contents. */
    public String currentGeneration() {
        requireOpen();
        return generation();
    }

    public ClipboardSnapshot read() throws ClipboardException {
        requireOpen();
        if (!policy.allowRead) throw failure("denied", "clipboard read is disabled");
        String before = generation();
        ClipData clip = manager.getPrimaryClip();
        List<ClipboardItem> items = new ArrayList<>();
        if (clip != null) {
            if (clip.getItemCount() > ClipboardPolicy.MAX_ITEMS) throw failure("limit", "clipboard item limit exceeded");
            rejectUnsupportedDescription(clip.getDescription());
            for (int i = 0; i < clip.getItemCount(); i++) items.add(readItem(clip.getItemAt(i)));
        }
        ClipboardValidation.validateItems(items, policy.maxBytes, policy.allowedFormats);
        if (!before.equals(generation())) throw failure("changed", "clipboard changed during read");
        return new ClipboardSnapshot(before, items);
    }

    public ClipboardReceipt write(String expectedGeneration, List<ClipboardItem> items) throws ClipboardException {
        requireOpen();
        if (!policy.allowWrite) throw failure("denied", "clipboard write is disabled");
        if (expectedGeneration == null || expectedGeneration.isEmpty()) throw failure("invalid", "expected generation is required");
        ClipboardValidation.validateItems(items, policy.maxBytes, policy.allowedFormats);
        if (!generation().equals(expectedGeneration)) throw failure("changed", "clipboard generation changed");
        pendingOwnChange = true;
        long next = change + 1;
        if (items.isEmpty()) {
            if (Build.VERSION.SDK_INT < 28) {
                pendingOwnChange = false;
                throw failure("unsupported", "clearing the clipboard requires Android 9 or later");
            }
            manager.clearPrimaryClip();
        } else {
            manager.setPrimaryClip(nativeClip(items));
        }
        change = next;
        return new ClipboardReceipt(generation(), "dispatched");
    }

    public ClipboardReceipt clear(String expectedGeneration) throws ClipboardException {
        return write(expectedGeneration, java.util.Collections.<ClipboardItem>emptyList());
    }

    @Override public void close() {
        requireUI();
        if (closed) return;
        closed = true;
        manager.removePrimaryClipChangedListener(listener);
    }

    private ClipboardItem readItem(ClipData.Item item) throws ClipboardException {
        List<ClipboardRepresentation> values = new ArrayList<>();
        if (item.getText() != null) add(values, TEXT, item.getText().toString().getBytes(StandardCharsets.UTF_8));
        if (item.getHtmlText() != null) add(values, HTML, item.getHtmlText().getBytes(StandardCharsets.UTF_8));
        if (item.getUri() != null) add(values, URI, item.getUri().toString().getBytes(StandardCharsets.UTF_8));
        if (item.getIntent() != null) add(values, INTENT, item.getIntent().toUri(Intent.URI_INTENT_SCHEME).getBytes(StandardCharsets.UTF_8));
        if (values.isEmpty()) throw failure("unsupported", "clipboard item has no lossless supported representation");
        return new ClipboardItem(values);
    }

    private void add(List<ClipboardRepresentation> values, String format, byte[] data) throws ClipboardException {
        if (!policy.allowedFormats.contains(format)) throw failure("denied", "clipboard contains an ungranted format");
        values.add(new ClipboardRepresentation(format, data));
    }

    private void rejectUnsupportedDescription(ClipDescription description) throws ClipboardException {
        Set<String> nativeFormats = new LinkedHashSet<>();
        nativeFormats.add(TEXT); nativeFormats.add(HTML); nativeFormats.add(URI); nativeFormats.add(INTENT);
        for (int i = 0; i < description.getMimeTypeCount(); i++) {
            if (!nativeFormats.contains(description.getMimeType(i))) {
                throw failure("unsupported", "clipboard advertises an unsupported native format");
            }
        }
    }

    private ClipData nativeClip(List<ClipboardItem> items) throws ClipboardException {
        List<ClipData.Item> nativeItems = new ArrayList<>();
        Set<String> formats = new LinkedHashSet<>();
        for (ClipboardItem item : items) {
            Map<String, byte[]> values = new LinkedHashMap<>();
            for (ClipboardRepresentation representation : item.representations) {
                values.put(representation.format, representation.data);
                formats.add(representation.format);
            }
            CharSequence text = decode(values.get(TEXT));
            String html = decode(values.get(HTML));
            Uri uri = values.containsKey(URI) ? Uri.parse(decode(values.get(URI))) : null;
            Intent intent = null;
            if (values.containsKey(INTENT)) {
                try { intent = Intent.parseUri(decode(values.get(INTENT)), Intent.URI_INTENT_SCHEME); }
                catch (java.net.URISyntaxException error) { throw failure("invalid", "invalid clipboard intent"); }
            }
            if (values.size() != count(text, html, uri, intent)) throw failure("unsupported", "unsupported Android clipboard format");
            nativeItems.add(new ClipData.Item(text, html, intent, uri));
        }
        String[] mimeTypes = formats.toArray(new String[0]);
        ClipData clip = new ClipData(new ClipDescription("ext", mimeTypes), nativeItems.get(0));
        for (int i = 1; i < nativeItems.size(); i++) clip.addItem(nativeItems.get(i));
        return clip;
    }

    private static int count(Object... values) { int count = 0; for (Object value : values) if (value != null) count++; return count; }
    private static String decode(byte[] value) { return value == null ? null : new String(value, StandardCharsets.UTF_8); }
    private String generation() { return producer + ":" + change; }
    private void requireOpen() { requireUI(); if (closed) throw new IllegalStateException("clipboard is closed"); }
    private static void requireUI() { if (Looper.myLooper() != Looper.getMainLooper()) throw new IllegalStateException("clipboard requires the UI thread"); }
    private static ClipboardException failure(String code, String message) { return new ClipboardException(code, message); }
}
