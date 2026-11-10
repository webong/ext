package io.github.webong.ext.system.fixtures;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * Loads the language-neutral vectors of res/system/testdata, which the Go tests run too. Set
 * EXT_SYSTEM_TESTDATA to use a copy elsewhere; otherwise the working directory is the repository.
 */
public final class Fixtures {
    private Fixtures() {}

    public static Path directory() {
        String path = System.getenv("EXT_SYSTEM_TESTDATA");
        return Paths.get(path != null && !path.isEmpty() ? path : "res/system/testdata");
    }

    /** testdata/{area}/v{version}/cases.json. */
    public static Map<String, Object> load(String area, int version) throws IOException {
        Path file = directory().resolve(area).resolve("v" + version).resolve("cases.json");
        return Json.object(Json.parse(new String(Files.readAllBytes(file), StandardCharsets.UTF_8)));
    }

    /** The error codes of testdata/codes.json. */
    public static Set<String> codes() throws IOException {
        Path file = directory().resolve("codes.json");
        Map<String, Object> doc = Json.object(Json.parse(new String(Files.readAllBytes(file), StandardCharsets.UTF_8)));
        Set<String> out = new HashSet<>();
        for (Object code : Json.array(doc.get("codes"))) {
            out.add((String) code);
        }
        return out;
    }

    public static List<Object> cases(Map<String, Object> file, String key) {
        List<Object> cases = Json.array(file.get(key));
        if (cases.isEmpty()) {
            throw new AssertionError(key + " has no cases");
        }
        return cases;
    }
}
