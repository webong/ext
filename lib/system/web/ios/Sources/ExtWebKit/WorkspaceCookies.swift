import Foundation
import ExtWeb
@preconcurrency import WebKit

/// Native cookie conversion is deliberately kept in the WebKit target.
enum WorkspaceCookies {
    static func native(_ bundle: CookieBundle) throws -> HTTPCookie {
        try CookieRules.validate(bundle)
        let c = bundle.cookie
        try CookieWorkflows.requirePortableScope(c)
        try CookieWorkflows.requireHeaderValue(c.value)
        guard c.name.utf8.allSatisfy({ $0 < 128 }), c.domain.utf8.allSatisfy({ (0x30...0x39).contains($0) || (0x41...0x5a).contains($0) || (0x61...0x7a).contains($0) || [0x2d, 0x2e, 0x3a].contains($0) }),
            c.path.utf8.allSatisfy({ $0 >= 0x21 && $0 <= 0x7e && $0 != 0x3b }),
            let url = URL(string: bundle.site) else {
            throw CookieError(message: "cookie cannot be represented by the native store")
        }
        var header = c.name + "=" + c.value + "; Path=" + c.path
        if c.domain.hasPrefix(".") { header += "; Domain=" + c.domain }
        if c.secure { header += "; Secure" }
        if c.httpOnly { header += "; HttpOnly" }
        if c.expiry != 0 {
            let format = DateFormatter()
            format.locale = Locale(identifier: "en_US_POSIX")
            format.timeZone = TimeZone(secondsFromGMT: 0)
            format.dateFormat = "EEE, dd MMM yyyy HH:mm:ss 'GMT'"
            header += "; Expires=" + format.string(from: Date(timeIntervalSince1970: Double(c.expiry)))
        }
        if !c.sameSitePolicy.isEmpty && c.sameSitePolicy != "unspecified" {
            header += "; SameSite=" + c.sameSitePolicy
        }
        guard let native = HTTPCookie.cookies(withResponseHeaderFields: ["Set-Cookie": header], for: url).first, native.name == c.name, native.value == c.value,
            native.domain == c.domain, native.path == c.path, native.isSecure == c.secure,
            native.isHTTPOnly == c.httpOnly,
            c.expiry == 0 ? native.expiresDate == nil : native.expiresDate?.timeIntervalSince1970 == Double(c.expiry),
            c.sameSitePolicy.isEmpty || c.sameSitePolicy == "unspecified" || native.sameSitePolicy?.rawValue.lowercased() == c.sameSitePolicy else {
            throw CookieError(message: "native cookie conversion would change cookie fields")
        }
        return native
    }
}
