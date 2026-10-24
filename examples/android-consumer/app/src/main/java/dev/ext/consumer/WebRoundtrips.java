package dev.ext.consumer;

import io.github.webong.ext.web.Bookmarklet;
import io.github.webong.ext.web.Cookie;
import io.github.webong.ext.web.CookieException;
import io.github.webong.ext.web.CookieRules;
import io.github.webong.ext.web.Site;
import io.github.webong.ext.web.Userscript;
import io.github.webong.ext.web.UserscriptRecord;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;

/**
 * What an app does with the web rules, with answers that Go gave: they run on ART, on the minified
 * release build, where Android's case tables (ICU) and R8 could change what a desktop JDK shows.
 * Each failure is one line in the result; an empty result passes.
 */
public final class WebRoundtrips {
    private WebRoundtrips() {}

    private static void expect(List<String> failures, String what, Object want, Object got) {
        if (!want.equals(got)) {
            failures.add(what + ": want " + want + ", got " + got);
        }
    }

    public static List<String> run() throws Exception {
        List<String> failures = new ArrayList<>();

        // Case folding goes through Character.toLowerCase, which Android takes from ICU.
        Site site = Site.parse("HTTPS://BÜCHER.example:8443/a%20b?x#f");
        expect(failures, "site", "https://b%C3%BCcher.example:8443/a%20b?x#f", site.string());
        expect(failures, "hostname", "bücher.example", site.hostname);
        Site greek = Site.parse("https://ΣΣ.example/");
        expect(failures, "sigma host", "σσ.example", greek.hostname);

        UserscriptRecord folded = Userscript.inferMetadata(new UserscriptRecord("t", "", "İK-X  y.", null, null, ""), "");
        expect(failures, "id folding", "ik-x-y", folded.id);

        // The digest of the baseline vector in res/web/testdata/userscript/v1alpha1/revision.json.
        UserscriptRecord baseline = new UserscriptRecord("session", "demo", "demo", Arrays.asList("https://example.com/*"),
                Collections.<String>emptyList(), "console.log(1);\n");
        expect(failures, "revision", "sha256:7443a61d70e39e9c2e0d0f04f58784af3fbcc0f3c5caa9f10f12f6cfa1092eb3",
                Userscript.revision(baseline));
        UserscriptRecord signed = baseline.signed();
        Userscript.validate(signed);
        String script = Userscript.replaySource(signed.id, signed.revision, signed.source, signed.matches, signed.excludeMatches);
        if (!script.contains("[\"demo\",\"" + signed.revision + "\",\"console.log(1);\\n\",[\"https://example.com/*\"],[]]")) {
            failures.add("replay script does not carry its values");
        }

        expect(failures, "bookmarklet", "javascript:void%20%28function%28%29%7B%0Aalert%281%29%0A%7D%29.call%28window%29%3B",
                Bookmarklet.encode("alert(1)"));
        expect(failures, "bookmarklet round trip", "a + b % 2;\n", Bookmarklet.decode(Bookmarklet.encode("a + b % 2;\n")));

        Cookie cookie = Cookie.builder("sid", "s3cr3t-value-0f7a", ".example.com", "/").secure(true).build();
        CookieRules.validate(cookie);
        if (cookie.toString().contains("s3cr3t")) {
            failures.add("a cookie description printed its value");
        }
        expect(failures, "cookie matches", Boolean.TRUE,
                CookieRules.matches(Site.parse("https://a.example.com/x"), cookie, false));
        try {
            CookieRules.validate(Cookie.builder("a", "v", "fe80::1%eth0", "/").build());
            failures.add("an IPv6 domain with a zone was accepted");
        } catch (CookieException expected) {
            // refused, as Go does
        }
        return failures;
    }
}
