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
    public List<String> servers() { return servers; }
    public List<String> search() { return search; }
    public List<String> options() { return options; }
}
