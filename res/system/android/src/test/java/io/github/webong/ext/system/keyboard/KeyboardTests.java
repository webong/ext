package io.github.webong.ext.system.keyboard;

public final class KeyboardTests {
    public static void main(String[] arguments) throws Exception {
        FakeConnection connection = new FakeConnection();
        KeyboardEditor editor = new KeyboardEditor(connection);

        connection.selection = "  value \n";
        assert "value".equals(editor.selection());
        connection.selection = " \n\t ";
        assert editor.selection() == null;
        connection.selection = "0123456789012345678901234567890";
        KeyboardEditor narrow = new KeyboardEditor(connection, new KeyboardLimits(8, 8));
        assert narrow.selection() == null;

        KeyboardEditor bounded = new KeyboardEditor(connection, new KeyboardLimits(20_000, 8));
        assert bounded.insert("hello");
        assert connection.insertions.equals("hello");
        expectInvalid(() -> bounded.insert(""));
        expectInvalid(() -> bounded.insert("nul\u0000"));
        expectLimit(() -> bounded.insert("too long for bounds"));
        expectInvalid(() -> bounded.deleteBackward(-1));
        bounded.deleteBackward(2);
        assert connection.backwardDeletes == 2;
        bounded.deleteForward(1);
        assert connection.forwardDeletes == 1;
        assert bounded.submit();
        assert connection.actions == 1;

        connection.before = "0123456789";
        connection.after = "abcdefghij";
        assert "6789".equals(editor.contextBefore(4));
        assert "abcd".equals(editor.contextAfter(4));
        assert editor.contextBefore(0) == null;
    }

    private interface Check { void run() throws Exception; }

    private static void expectInvalid(Check check) throws Exception {
        boolean invalid = false;
        try { check.run(); }
        catch (KeyboardException expected) { invalid = "invalid".equals(expected.kind()); }
        assert invalid;
    }

    private static void expectLimit(Check check) throws Exception {
        boolean limited = false;
        try { check.run(); }
        catch (KeyboardException expected) { limited = "limit".equals(expected.kind()); }
        assert limited;
    }

    private static final class FakeConnection implements KeyboardConnection {
        String selection;
        String before;
        String after;
        String insertions = "";
        int backwardDeletes;
        int forwardDeletes;
        int actions;

        @Override public CharSequence selectedText() { return selection; }
        @Override public CharSequence textBefore(int characters) {
            return before == null ? null : before.substring(Math.max(0, before.length() - characters));
        }
        @Override public CharSequence textAfter(int characters) {
            return after == null ? null : after.substring(0, Math.min(after.length(), characters));
        }
        @Override public boolean insert(CharSequence text) { insertions = text.toString(); return true; }
        @Override public boolean deleteBackward(int count) { backwardDeletes += count; return true; }
        @Override public boolean deleteForward(int count) { forwardDeletes += count; return true; }
        @Override public boolean performEditorAction() { actions += 1; return true; }
    }

    private KeyboardTests() {}
}
