package io.github.webong.ext.system.share;

import java.util.List;
import java.util.Objects;

/** One contribution from an Android share intent after intent parsing. */
public final class ShareLoadedItem {
    private final String subject;
    private final String text;
    private final List<String> urls;
    private final List<ShareAttachment> attachments;

    public ShareLoadedItem(String subject, String text, List<String> urls, List<ShareAttachment> attachments) {
        this.subject = subject;
        this.text = text;
        this.urls = urls == null ? List.of() : List.copyOf(urls);
        this.attachments = attachments == null ? List.of() : List.copyOf(attachments);
    }

    public String subject() { return subject; }
    public String text() { return text; }
    public List<String> urls() { return urls; }
    public List<ShareAttachment> attachments() { return attachments; }

    @Override public boolean equals(Object other) {
        if (!(other instanceof ShareLoadedItem)) return false;
        ShareLoadedItem value = (ShareLoadedItem) other;
        return Objects.equals(subject, value.subject) && Objects.equals(text, value.text)
                && urls.equals(value.urls) && attachments.equals(value.attachments);
    }

    @Override public int hashCode() { return Objects.hash(subject, text, urls, attachments); }
}
