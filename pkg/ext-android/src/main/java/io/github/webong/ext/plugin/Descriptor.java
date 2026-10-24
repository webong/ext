package io.github.webong.ext.plugin;

import java.nio.charset.StandardCharsets;
import java.util.List;

/**
 * The ext.plugin/v1 descriptor a guest serves in its handshake. {@code reverse} lists
 * the host operations the plugin may call ({@link Call#reverse()}); it is empty for a
 * plugin that calls none.
 */
public record Descriptor(Identity identity, List<Contract> contracts, List<Contract> reverse) {
    public static final String API_VERSION = "ext.plugin/v1";

    public Descriptor(Identity identity, List<Contract> contracts) {
        this(identity, contracts, List.of());
    }

    byte[] toJson() {
        StringBuilder out = new StringBuilder("{\"apiVersion\":");
        Json.quote(out, API_VERSION).append(",\"identity\":{\"id\":");
        Json.quote(out, identity.id()).append(",\"revision\":");
        Json.quote(out, identity.revision());
        if (identity.version() != null) {
            Json.quote(out.append(",\"version\":"), identity.version());
        }
        out.append("},\"contracts\":");
        contracts(out, contracts);
        if (reverse != null && !reverse.isEmpty()) {
            contracts(out.append(",\"reverse\":"), reverse);
        }
        return out.append('}').toString().getBytes(StandardCharsets.UTF_8);
    }

    private static void contracts(StringBuilder out, List<Contract> list) {
        out.append('[');
        for (int i = 0; i < list.size(); i++) {
            Contract contract = list.get(i);
            out.append(i == 0 ? "" : ",").append("{\"name\":");
            Json.quote(out, contract.name()).append(",\"version\":");
            Json.quote(out, contract.version()).append(",\"operations\":[");
            for (int j = 0; j < contract.operations().size(); j++) {
                Operation operation = contract.operations().get(j);
                out.append(j == 0 ? "" : ",").append("{\"name\":");
                Json.quote(out, operation.name());
                if (operation.surface() != null) {
                    Json.quote(out.append(",\"surface\":"), operation.surface());
                }
                if (operation.progress()) {
                    out.append(",\"progress\":true");
                }
                out.append('}');
            }
            out.append("]}");
        }
        out.append(']');
    }
}
