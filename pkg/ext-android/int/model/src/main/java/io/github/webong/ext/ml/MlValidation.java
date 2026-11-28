package io.github.webong.ext.ml;

public final class MlValidation {
    public static boolean isValidName(String value, String what) {
        if (value == null || value.isEmpty() || value.length() > MlContract.MAX_NAME_LENGTH) return false;
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            if (c < ' ' || c == '\u007f' || c == '/' || c == '\\') return false;
        }
        if (value.charAt(0) == '.' || value.charAt(0) == '-' || value.charAt(0) == '_') return false;
        return value.trim().equals(value);
    }

    public static boolean isValidVersion(String value) {
        if (value == null || value.isEmpty() || value.length() > MlContract.MAX_NAME_LENGTH) return false;
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            if (c < ' ' || c == '\u007f' || c == '/' || c == '\\') return false;
        }
        return value.trim().equals(value);
    }

    public static boolean isHexSha256(String value) {
        if (value == null || value.length() != 64) return false;
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'))) return false;
        }
        return true;
    }
}
