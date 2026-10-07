package io.github.webong.ext.plugin;

import java.nio.charset.StandardCharsets;
import java.util.List;

/** The ext.plugin/v1 descriptor a guest serves in its handshake. */
public record Descriptor(Identity identity, List<Contract> contracts) {
    public static final String API_VERSION = "ext.plugin/v1";

    byte[] toJson() {
        StringBuilder out = new StringBuilder("{\"apiVersion\":");
        Json.quote(out, API_VERSION).append(",\"identity\":{\"id\":");
        Json.quote(out, identity.id()).append(",\"revision\":");
        Json.quote(out, identity.revision());
        if (identity.version() != null) {
            Json.quote(out.append(",\"version\":"), identity.version());
        }
        out.append("},\"contracts\":[");
        for (int i = 0; i < contracts.size(); i++) {
            Contract contract = contracts.get(i);
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
                out.append('}');
            }
            out.append("]}");
        }
        return out.append("]}").toString().getBytes(StandardCharsets.UTF_8);
    }
}
