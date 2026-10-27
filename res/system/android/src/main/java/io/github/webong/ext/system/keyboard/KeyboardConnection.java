package io.github.webong.ext.system.keyboard;

/**
 * The text-document operations a keyboard extension can perform. Android's
 * {@code InputConnection} is adapted by {@code SystemKeyboard}; a test or
 * another host can supply its own.
 */
public interface KeyboardConnection {
    CharSequence selectedText();

    CharSequence textBefore(int characters);

    CharSequence textAfter(int characters);

    boolean insert(CharSequence text);

    boolean deleteBackward(int count);

    boolean deleteForward(int count);

    boolean performEditorAction();
}
