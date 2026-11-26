package io.github.webong.ext.system.android;

import android.inputmethodservice.InputMethodService;
import android.os.Build;
import android.view.inputmethod.InputConnection;
import android.view.inputmethod.InputMethodManager;

import io.github.webong.ext.system.keyboard.KeyboardConnection;
import io.github.webong.ext.system.keyboard.KeyboardEditor;

/**
 * Bounded {@link android.inputmethodservice.InputMethodService} helpers for
 * reading selection and context, inserting and deleting text, submitting the
 * editor action, and switching keyboards.
 *
 * <p>The caller owns the input view, actions, strings, and any capability or
 * application handoff. This class only adapts the native input connection.
 */
public final class SystemKeyboard implements KeyboardConnection {
    private final InputMethodService service;

    public SystemKeyboard(InputMethodService service) {
        if (service == null) throw new IllegalArgumentException("input method service is required");
        this.service = service;
    }

    public KeyboardEditor editor() {
        return new KeyboardEditor(this);
    }

    public InputMethodService service() { return service; }

    private InputConnection connection() {
        return service.getCurrentInputConnection();
    }

    @Override public CharSequence selectedText() {
        InputConnection connection = connection();
        return connection == null ? null : connection.getSelectedText(0);
    }

    @Override public CharSequence textBefore(int characters) {
        InputConnection connection = connection();
        return connection == null || characters < 1 ? null : connection.getTextBeforeCursor(characters, 0);
    }

    @Override public CharSequence textAfter(int characters) {
        InputConnection connection = connection();
        return connection == null || characters < 1 ? null : connection.getTextAfterCursor(characters, 0);
    }

    @Override public boolean insert(CharSequence text) {
        InputConnection connection = connection();
        return connection != null && text != null && connection.commitText(text, 1);
    }

    @Override public boolean deleteBackward(int count) {
        InputConnection connection = connection();
        if (connection == null || count < 0) return false;
        return connection.deleteSurroundingText(count, 0);
    }

    @Override public boolean deleteForward(int count) {
        InputConnection connection = connection();
        if (connection == null || count < 0) return false;
        return connection.deleteSurroundingText(0, count);
    }

    @Override public boolean performEditorAction() {
        return service.sendDefaultEditorAction(true);
    }

    /**
     * Advances to the next installed keyboard on API 28 and newer, otherwise
     * opens the system picker. It never changes application settings.
     */
    public boolean switchKeyboard() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            try {
                if (service.switchToNextInputMethod(false)) return true;
            } catch (RuntimeException ignored) {
                // Fall through to the system picker.
            }
        }
        Object manager = service.getSystemService(InputMethodService.INPUT_METHOD_SERVICE);
        if (!(manager instanceof InputMethodManager)) return false;
        ((InputMethodManager) manager).showInputMethodPicker();
        return true;
    }

    /** Reports whether the current editor has a non-blank selection. */
    public boolean hasSelection() {
        CharSequence value = selectedText();
        return value != null && !value.toString().trim().isEmpty();
    }
}
