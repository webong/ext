package io.github.webong.ext.network;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

public final class NetworkResolver {
    private final List<String> servers;
    private final List<String> search;
    private final List<String> options;
    public NetworkResolver(List<String> servers, List<String> search, List<String> options) {
        this.servers = Collections.unmodifiableList(new ArrayList<>(servers));
        this.search = Collections.unmodifiableList(new ArrayList<>(search));
        this.options = Collections.unmodifiableList(new ArrayList<>(options));
    }
    /**
     * Trims each entry (leading and trailing U+0009 to U+000D and U+0020) and drops empty and
     * repeated entries, keeping the first of each and the order the system reported
     * (testdata/network/v1).
     */
    public static List<String> clean(List<String> values) {
        List<String> out = new ArrayList<>();
        java.util.Set<String> seen = new java.util.HashSet<>();
        for (String value : values) {
            int start = 0;
            int end = value.length();
            while (start < end && space(value.charAt(start))) start++;
            while (end > start && space(value.charAt(end - 1))) end--;
            String trimmed = value.substring(start, end);
            if (!trimmed.isEmpty() && seen.add(trimmed)) out.add(trimmed);
        }
        return out;
    }

    private static boolean space(char character) {
        return character == ' ' || character >= '\t' && character <= '\r';
    }

    public List<String> servers() { return servers; }
    public List<String> search() { return search; }
    public List<String> options() { return options; }
}
