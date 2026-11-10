package io.github.webong.ext.system;

import java.util.regex.Pattern;

/** The wire rules the surface models share (testdata/surface/v1). */
final class SurfaceWire {
    private static final Pattern TIMESTAMP = Pattern.compile(
        "\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}(\\.\\d+)?(Z|[+-]\\d{2}:\\d{2})");

    private SurfaceWire() {}

    /** An RFC 3339 timestamp. */
    static boolean timestamp(String value) {
        return value != null && TIMESTAMP.matcher(value).matches();
    }
}
