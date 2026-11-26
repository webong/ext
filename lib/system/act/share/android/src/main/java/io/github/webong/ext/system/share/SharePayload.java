package io.github.webong.ext.system.share;

import java.util.List;
import java.util.Objects;

/**
 * The normalized, deduplicated share result. The embedding application owns the
 * destination and any further transport; this model adds no event schema,
 * capability inbox, or application identity.
 */
public final class SharePayload {
    private final String subject;
    private final String text;
    private final List<String> urls;
    private final List<ShareAttachment> attachments;
    private final long byteCount;

    public SharePayload(String subject, String text, List<String> urls,
            List<ShareAttachment> attachments, long byteCount) {
        this.subject = subject;
        this.text = text;
        this.urls = urls == null ? List.of() : List.copyOf(urls);
        this.attachments = attachments == null ? List.of() : List.copyOf(attachments);
        this.byteCount = byteCount;
    }

    public String subject() { return subject; }
    public String text() { return text; }
    public List<String> urls() { return urls; }
    public List<ShareAttachment> attachments() { return attachments; }
    public long byteCount() { return byteCount; }

    @Override public boolean equals(Object other) {
        if (!(other instanceof SharePayload)) return false;
        SharePayload value = (SharePayload) other;
        return Objects.equals(subject, value.subject) && Objects.equals(text, value.text)
                && urls.equals(value.urls) && attachments.equals(value.attachments)
                && byteCount == value.byteCount;
    }

    @Override public int hashCode() { return Objects.hash(subject, text, urls, attachments, byteCount); }
}
