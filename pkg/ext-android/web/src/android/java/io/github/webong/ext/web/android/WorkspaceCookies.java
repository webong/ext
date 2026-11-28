package io.github.webong.ext.web.android;

import io.github.webong.ext.web.Cookie;
import io.github.webong.ext.web.CookieBundle;
import io.github.webong.ext.web.CookieException;
import io.github.webong.ext.web.CookieRules;
import io.github.webong.ext.web.CookieWorkflows;
import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.Locale;
import java.util.TimeZone;

final class WorkspaceCookies {
    private WorkspaceCookies() {}
    static String header(CookieBundle bundle) throws CookieException {
        CookieRules.validate(bundle);
        Cookie c = bundle.cookie;
        CookieWorkflows.requirePortableScope(c);
        CookieWorkflows.requireHeaderValue(c.value);
        for (char ch : c.name.toCharArray()) if (ch > 127) throw invalid();
        if (!c.domain.matches("[a-zA-Z0-9.:-]+")) throw invalid();
        for (char ch : c.path.toCharArray()) if (ch < 0x21 || ch > 0x7e || ch == ';') throw invalid();
        StringBuilder out = new StringBuilder(c.name).append('=').append(c.value).append("; Path=").append(c.path);
        if (c.domain.startsWith(".")) out.append("; Domain=").append(c.domain);
        if (c.secure) out.append("; Secure");
        if (c.httpOnly) out.append("; HttpOnly");
        if (!c.sameSitePolicy.isEmpty() && !c.sameSitePolicy.equals("unspecified")) out.append("; SameSite=").append(c.sameSitePolicy);
        if (c.expiry != 0) {
            SimpleDateFormat format = new SimpleDateFormat("EEE, dd MMM yyyy HH:mm:ss 'GMT'", Locale.US);
            format.setTimeZone(TimeZone.getTimeZone("GMT"));
            out.append("; Expires=").append(format.format(new Date(c.expiry * 1000)));
        }
        return out.toString();
    }
    private static CookieException invalid() { return new CookieException("cookie cannot be represented by the native store"); }
}
