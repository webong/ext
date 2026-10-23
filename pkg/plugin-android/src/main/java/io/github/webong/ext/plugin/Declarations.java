package io.github.webong.ext.plugin;

import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * What the descriptor declares about progress and reverse calls, read from the descriptor the
 * engine validated and serves, so a call and the handshake agree on it.
 */
final class Declarations {
    private final Set<String> progress = new HashSet<>();
    private final Set<String> reverse = new HashSet<>();

    static Declarations of(byte[] descriptorJson) {
        Declarations declared = new Declarations();
        Map<String, Object> descriptor = Json.object(Json.parse(descriptorJson));
        declared.read(descriptor.get("contracts"), declared.progress, true);
        declared.read(descriptor.get("reverse"), declared.reverse, false);
        return declared;
    }

    private void read(Object contracts, Set<String> into, boolean progressOnly) {
        if (!(contracts instanceof List<?> list)) {
            return;
        }
        for (Object item : list) {
            Map<String, Object> contract = Json.object(item);
            if (contract == null || !(contract.get("operations") instanceof List<?> operations)) {
                continue;
            }
            for (Object o : operations) {
                Map<String, Object> operation = Json.object(o);
                if (operation != null && (!progressOnly || Boolean.TRUE.equals(operation.get("progress")))) {
                    into.add(key(Json.string(contract.get("name")), Json.string(contract.get("version")),
                            Json.string(operation.get("name"))));
                }
            }
        }
    }

    private static String key(String contract, String version, String operation) {
        return contract + '\0' + version + '\0' + operation;
    }

    /** The operation reports progress. */
    boolean progress(ContractRef contract, String operation) {
        return progress.contains(key(contract.name(), contract.version(), operation));
    }

    /** The plugin may call this host operation. */
    boolean reverse(ContractRef contract, String operation) {
        return reverse.contains(key(contract.name(), contract.version(), operation));
    }
}
