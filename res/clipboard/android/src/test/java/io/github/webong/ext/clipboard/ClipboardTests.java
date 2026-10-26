package io.github.webong.ext.clipboard;

import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.Collections;

public final class ClipboardTests {
    private static int checks;
    private static void check(boolean value, String name) { checks++; if (!value) throw new AssertionError(name); }

    public static void main(String[] args) throws Exception {
        ClipboardPolicy policy = new ClipboardPolicy(5, Collections.singletonList("text/plain"), true, true);
        check(policy.maxBytes == 5, "policy");
        ClipboardItem item = new ClipboardItem(Collections.singletonList(
                new ClipboardRepresentation("text/plain", "hello".getBytes(StandardCharsets.UTF_8))));
        ClipboardValidation.validateItems(Collections.singletonList(item), 5, policy.allowedFormats);
        try { ClipboardValidation.validateItems(Collections.singletonList(item), 4, policy.allowedFormats); check(false, "limit rejected"); }
        catch (ClipboardException expected) { check(expected.code.equals("limit"), "limit code"); }
        try { new ClipboardPolicy(5, Arrays.asList("text/plain", "text/plain"), true, true); check(false, "duplicate rejected"); }
        catch (ClipboardException expected) { check(expected.code.equals("invalid"), "duplicate code"); }
        byte[] input = new byte[] {1};
        ClipboardRepresentation copy = new ClipboardRepresentation("text/plain", input);
        input[0] = 2;
        check(copy.data[0] == 1, "defensive data copy");
        System.out.println(checks + " checks, 0 failures");
    }
}
