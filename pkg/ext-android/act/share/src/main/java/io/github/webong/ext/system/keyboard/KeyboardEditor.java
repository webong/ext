package io.github.webong.ext.system.keyboard;

/**
 * Bounded helpers around a keyboard's text connection. Selection is trimmed and
 * a blank selection reports {@code null}; an oversized selection reports
 * {@code null} rather than copying unbounded text. A character is a Unicode code point. The editor never chooses a
 * keyboard, launches an application, or records an event.
 */
public final class KeyboardEditor {
    private final KeyboardConnection connection;
    private final KeyboardLimits limits;

    public KeyboardEditor(KeyboardConnection connection) {
        this(connection, new KeyboardLimits());
    }

    public KeyboardEditor(KeyboardConnection connection, KeyboardLimits limits) {
        if (connection == null || limits == null) {
            throw new IllegalArgumentException("keyboard connection and limits are required");
        }
        this.connection = connection;
        this.limits = limits;
    }

    public String selection() {
        CharSequence value = connection.selectedText();
        if (value == null) return null;
        String trimmed = trim(value.toString());
        if (trimmed.isEmpty() || characters(trimmed) > limits.maxSelectionChars) return null;
        return trimmed;
    }

    public String contextBefore(int limit) {
        return window(connection.textBefore(Math.max(1, limit)), limit, false);
    }

    public String contextAfter(int limit) {
        return window(connection.textAfter(Math.max(1, limit)), limit, true);
    }

    public boolean insert(String text) throws KeyboardException {
        if (text == null || text.isEmpty() || text.indexOf('\0') >= 0) {
            throw new KeyboardException("invalid", "keyboard text must be non-empty UTF-16 without NUL");
        }
        if (characters(text) > limits.maxInsertChars) {
            throw new KeyboardException("too_large",
                    "keyboard text exceeds the bound of " + limits.maxInsertChars + " characters");
        }
        if (!connection.insert(text)) {
            throw new KeyboardException("unavailable", "keyboard text could not be inserted");
        }
        return true;
    }

    /** Submits the editor's default action; the host decides what it means. */
    public boolean submit() throws KeyboardException {
        if (!connection.performEditorAction()) {
            throw new KeyboardException("unavailable", "editor action is unavailable");
        }
        return true;
    }

    public void deleteBackward(int count) throws KeyboardException {
        validateDelete(count);
        if (!connection.deleteBackward(count)) {
            throw new KeyboardException("unavailable", "backward deletion was not accepted");
        }
    }

    public void deleteForward(int count) throws KeyboardException {
        validateDelete(count);
        if (!connection.deleteForward(count)) {
            throw new KeyboardException("unavailable", "forward deletion was not accepted");
        }
    }

    private void validateDelete(int count) throws KeyboardException {
        if (count < 0) throw new KeyboardException("invalid", "delete count must not be negative");
        if (count > limits.maxInsertChars) {
            throw new KeyboardException("too_large",
                    "delete count exceeds the bound of " + limits.maxInsertChars);
        }
    }

    private static String window(CharSequence value, int limit, boolean head) {
        if (value == null || limit < 1 || value.length() == 0) return null;
        String text = value.toString();
        int count = characters(text);
        if (count <= limit) return text;
        return head ? text.substring(0, text.offsetByCodePoints(0, limit))
                : text.substring(text.offsetByCodePoints(0, count - limit));
    }

    private static int characters(String text) {
        return text.codePointCount(0, text.length());
    }

    /** Strips leading and trailing U+0009 to U+000D and U+0020; the other controls are content. */
    private static String trim(String value) {
        int start = 0;
        int end = value.length();
        while (start < end && space(value.charAt(start))) start++;
        while (end > start && space(value.charAt(end - 1))) end--;
        return value.substring(start, end);
    }

    private static boolean space(char character) {
        return character == ' ' || character >= '\t' && character <= '\r';
    }
}
