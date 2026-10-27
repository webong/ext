package io.github.webong.ext.system.android;

import android.content.ClipData;
import android.content.ContentResolver;
import android.content.Context;
import android.content.Intent;
import android.database.Cursor;
import android.net.Uri;
import android.provider.OpenableColumns;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Set;

import io.github.webong.ext.system.share.ShareAttachment;
import io.github.webong.ext.system.share.ShareException;
import io.github.webong.ext.system.share.ShareLimits;
import io.github.webong.ext.system.share.ShareLoadedItem;
import io.github.webong.ext.system.share.ShareNormalizer;
import io.github.webong.ext.system.share.SharePayload;

/**
 * Normalizes {@code ACTION_SEND} and {@code ACTION_SEND_MULTIPLE} intents into
 * the bounded share model. The caller supplies the attachment directory and
 * owns the destination, application handoff, and any further processing.
 *
 * <p>It does not launch the application, record an event, choose storage
 * policy, or define a capability schema.
 */
public final class SystemShare {
    private static final int COPY_BUFFER = 64 * 1024;

    private final Context context;

    public SystemShare(Context context) {
        if (context == null) throw new IllegalArgumentException("share context is required");
        this.context = context.getApplicationContext() == null ? context : context.getApplicationContext();
    }

    public SharePayload capture(Intent intent, File directory, ShareLimits limits)
            throws ShareException {
        if (intent == null) throw new ShareException("invalid", "share intent is required");
        if (limits == null) throw new ShareException("invalid", "share limits are required");
        String action = intent.getAction();
        if (!Intent.ACTION_SEND.equals(action) && !Intent.ACTION_SEND_MULTIPLE.equals(action)) {
            throw new ShareException("unsupported", "intent is not a supported share action");
        }
        File destination = requireDirectory(directory);
        String subject = textExtra(intent, Intent.EXTRA_SUBJECT);
        String text = textExtra(intent, Intent.EXTRA_TEXT);
        if (text == null) text = clipText(intent.getClipData());

        List<ShareAttachment> attachments = new ArrayList<>();
        List<Uri> streams = sharedStreams(intent);
        long copied = 0;
        for (int index = 0; index < streams.size() && index < limits.maxItems; index++) {
            Uri uri = streams.get(index);
            String name = portableName(queryDisplayName(uri), index);
            String mimeType = contentType(uri, intent.getType());
            long remaining = limits.maxBytes - copied;
            if (remaining <= 0) {
                throw new ShareException("limit", "attachment bytes exceed the bound of " + limits.maxBytes);
            }
            File target = copy(uri, destination, name, index, remaining);
            long size = target.length();
            copied += size;
            attachments.add(new ShareAttachment(name, mimeType, size, null, target.getAbsolutePath()));
        }
        ShareLoadedItem item = new ShareLoadedItem(subject, text, List.of(), attachments);
        return ShareNormalizer.normalize(List.of(item), limits);
    }

    private static File requireDirectory(File directory) throws ShareException {
        if (directory == null) throw new ShareException("invalid", "an attachment directory is required");
        try {
            File canonical = directory.getCanonicalFile();
            if (!canonical.isDirectory()) {
                throw new ShareException("invalid", "attachment destination is not a directory");
            }
            return canonical;
        } catch (IOException error) {
            throw new ShareException("invalid", "attachment destination cannot be resolved", error);
        }
    }

    private static String textExtra(Intent intent, String key) {
        CharSequence value = intent.getCharSequenceExtra(key);
        if (value == null) return null;
        String text = value.toString();
        return text.isEmpty() ? null : text;
    }

    private static String clipText(ClipData clip) {
        if (clip == null) return null;
        for (int index = 0; index < clip.getItemCount(); index++) {
            CharSequence value = clip.getItemAt(index).getText();
            if (value != null && value.length() > 0) return value.toString();
        }
        return null;
    }

    @SuppressWarnings("deprecation")
    private static List<Uri> sharedStreams(Intent intent) {
        Set<Uri> values = new LinkedHashSet<>();
        ClipData clip = intent.getClipData();
        if (clip != null) {
            for (int index = 0; index < clip.getItemCount(); index++) {
                Uri uri = clip.getItemAt(index).getUri();
                if (uri != null) values.add(uri);
            }
        }
        if (!values.isEmpty()) return new ArrayList<>(values);
        List<Uri> extra = Intent.ACTION_SEND_MULTIPLE.equals(intent.getAction())
                ? intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM)
                : listOfNullable(intent.getParcelableExtra(Intent.EXTRA_STREAM));
        if (extra != null) {
            for (Uri uri : extra) {
                if (uri != null) values.add(uri);
            }
        }
        return new ArrayList<>(values);
    }

    private static List<Uri> listOfNullable(Uri uri) {
        return uri == null ? List.of() : List.of(uri);
    }

    private String queryDisplayName(Uri uri) {
        try (Cursor cursor = context.getContentResolver().query(
                uri, new String[] {OpenableColumns.DISPLAY_NAME}, null, null, null)) {
            if (cursor != null && cursor.moveToFirst()) {
                String value = cursor.getString(0);
                return value == null || value.isEmpty() ? null : value;
            }
        } catch (RuntimeException ignored) {
            // A provider may refuse the query; the fallback name is safe.
        }
        return null;
    }

    private String contentType(Uri uri, String fallback) {
        try {
            String value = context.getContentResolver().getType(uri);
            if (value != null && !value.isEmpty()) return value;
        } catch (RuntimeException ignored) {
            // Fall back to the intent's declared type.
        }
        return fallback == null || fallback.isEmpty() ? "application/octet-stream" : fallback;
    }

    private static String portableName(String value, int index) {
        String preferred = value == null || value.isEmpty() ? "attachment-" + index : value;
        String sanitized = preferred.replaceAll("[^A-Za-z0-9._-]", "_");
        if (!ShareNormalizer.portableName(sanitized)) return "attachment-" + index;
        return sanitized;
    }

    private File copy(Uri uri, File directory, String name, int index, long maximum)
            throws ShareException {
        File target = new File(directory, index + "-" + name);
        try {
            String directoryPath = directory.getCanonicalPath() + File.separator;
            if (!target.getCanonicalPath().startsWith(directoryPath)) {
                throw new ShareException("invalid", "attachment path escapes the destination");
            }
            ContentResolver resolver = context.getContentResolver();
            try (InputStream input = resolver.openInputStream(uri)) {
                if (input == null) {
                    throw new ShareException("load", "attachment could not be opened");
                }
                try (FileOutputStream output = new FileOutputStream(target)) {
                    byte[] buffer = new byte[COPY_BUFFER];
                    long written = 0;
                    for (int read; (read = input.read(buffer)) != -1; ) {
                        written += read;
                        if (written > maximum) {
                            throw new ShareException("limit",
                                    "attachment exceeds the remaining byte bound");
                        }
                        output.write(buffer, 0, read);
                    }
                }
            }
            return target;
        } catch (ShareException error) {
            deleteQuietly(target);
            throw error;
        } catch (IOException error) {
            deleteQuietly(target);
            throw new ShareException("load", "attachment could not be copied", error);
        }
    }

    private static void deleteQuietly(File target) {
        if (target != null && target.isFile()) {
            // Best effort: the caller still sees the original failure.
            //noinspection ResultOfMethodCallIgnored
            target.delete();
        }
    }
}
