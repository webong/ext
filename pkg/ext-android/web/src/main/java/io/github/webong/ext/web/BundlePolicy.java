package io.github.webong.ext.web;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

/** Resource policy for local bundles; not a capability sandbox. */
public final class BundlePolicy {
    public final boolean allowNet;
    public final List<String> allowOrigins;
    public BundlePolicy() { this(false, Collections.emptyList()); }
    public BundlePolicy(boolean allowNet, List<String> allowOrigins) {
        this.allowNet = allowNet;
        this.allowOrigins = Collections.unmodifiableList(new ArrayList<>(allowOrigins));
    }
    public String contentSecurityPolicy() {
        String connect = "'self'", resources = "'self' data: blob:";
        if (allowNet) { connect = "*"; resources = "* data: blob:"; }
        else if (!allowOrigins.isEmpty()) {
            StringBuilder extra = new StringBuilder();
            for (String origin : allowOrigins) {
                if (origin == null || !(origin.startsWith("http://") || origin.startsWith("https://"))) continue;
                int slashes = 0;
                boolean bad = false;
                for (int i = 0; i < origin.length(); i++) {
                    char c = origin.charAt(i);
                    if (c == '/') slashes++;
                    if (" ;,'\"\r\n\t".indexOf(c) >= 0) bad = true;
                }
                if (bad || slashes != 2) continue;
                if (extra.length() > 0) extra.append(' ');
                extra.append(origin);
            }
            connect += " " + extra; resources += " " + extra;
        }
        return "default-src 'none'; script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval' blob:; "
            + "style-src 'self' 'unsafe-inline'; connect-src " + connect
            + "; img-src " + resources + "; font-src " + resources + "; media-src " + resources
            + "; worker-src 'self' blob:; form-action 'none'; frame-src 'none'; object-src 'none'; base-uri 'self'";
    }
}
