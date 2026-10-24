import Foundation

/// A bookmarklet source or name that the portable rules refuse.
public struct BookmarkletError: Error, Equatable, CustomStringConvertible {
    public let message: String
    public var description: String { message }
}

/// Portable, user-activated browser scripts (`res/web/bookmarklet`, ext.bookmarklet/v1alpha1).
/// Nothing here evaluates a script or touches a browser's bookmarks.
public enum Bookmarklet {
    /// Preparation is bounded; a browser's bookmark URL limit may be lower.
    public static let maxSourceBytes = 64 * 1024

    private static func validate(_ source: String) throws {
        if GoText.isBlank(source) { throw BookmarkletError(message: "bookmarklet source is empty") }
        if source.utf8.count > maxSourceBytes { throw BookmarkletError(message: "bookmarklet source exceeds 64 KiB") }
        if source.utf8.contains(0) {
            throw BookmarkletError(message: "bookmarklet source must be UTF-8 without NUL bytes")
        }
    }

    /// Wraps a JavaScript function body so a string result cannot replace the document, and
    /// percent-encodes it into a `javascript:` URL. Newlines and literal percent signs are kept.
    public static func encode(_ source: String) throws -> String {
        try validate(source)
        if GoText.hasPrefix(GoText.lowercased(GoText.trimSpace(source)), "javascript:") {
            throw BookmarkletError(message: "expected JavaScript source; import a javascript: URL first")
        }
        let code = wrapperStart + source + wrapperEnd
        var out = "javascript:"
        let hex = Array("0123456789ABCDEF".utf8)
        var bytes: [UInt8] = []
        for byte in code.utf8 {
            let unreserved = (byte >= 0x30 && byte <= 0x39) || (byte >= 0x41 && byte <= 0x5A) || (byte >= 0x61 && byte <= 0x7A)
                || byte == UInt8(ascii: "-") || byte == UInt8(ascii: "_") || byte == UInt8(ascii: ".") || byte == UInt8(ascii: "~")
            if unreserved {
                bytes.append(byte)
            } else {
                bytes += [UInt8(ascii: "%"), hex[Int(byte >> 4)], hex[Int(byte & 15)]]
            }
        }
        out += String(decoding: bytes, as: UTF8.self)
        return out
    }

    /// Imports a `javascript:` URL as source for review, without evaluating it. It decodes
    /// exactly once, keeps a literal `+` and a `%` that is not an escape, and removes only the
    /// exact wrapper `encode` writes, so a round trip is lossless.
    public static func decode(_ url: String) throws -> String {
        let value = Array(GoText.trimSpace(url).utf8)
        let prefix = Array("javascript:".utf8)
        if value.count > 3 * (maxSourceBytes + 64) + prefix.count {
            throw BookmarkletError(message: "bookmarklet URL is too large")
        }
        guard value.count >= prefix.count,
            zip(value, prefix).allSatisfy({ asciiLower($0) == $1 })
        else {
            throw BookmarkletError(message: "bookmarklet URL must use javascript:")
        }
        let body = Array(value[prefix.count...])
        var decoded: [UInt8] = []
        decoded.reserveCapacity(body.count)
        var index = 0
        while index < body.count {
            if body[index] == UInt8(ascii: "%"), index + 2 < body.count,
                URLEscape.isHex(body[index + 1]), URLEscape.isHex(body[index + 2])
            {
                decoded.append(URLEscape.unhex(body[index + 1]) << 4 | URLEscape.unhex(body[index + 2]))
                index += 3
            } else {
                decoded.append(body[index])
                index += 1
            }
        }
        let start = Array(wrapperStart.utf8)
        let end = Array(wrapperEnd.utf8)
        if decoded.starts(with: start), decoded.suffix(end.count).elementsEqual(end) {
            // TrimPrefix, then TrimSuffix on what is left (the two can share their newline).
            decoded = Array(decoded.dropFirst(start.count))
            if decoded.suffix(end.count).elementsEqual(end) { decoded = Array(decoded.dropLast(end.count)) }
        }
        guard let source = GoText.decodeStrict(decoded) else {
            throw BookmarkletError(message: "bookmarklet source must be UTF-8 without NUL bytes")
        }
        try validate(source)
        return source
    }

    /// A standalone HTML page with a draggable bookmark link and the escaped source for
    /// review. There is no script that runs on page load.
    public static func installPage(name: String, source: String) throws -> String {
        if GoText.isBlank(name) || name.utf8.count > 256 || name.utf8.contains(0) {
            throw BookmarkletError(message: "bookmarklet name must be nonempty UTF-8, at most 256 bytes")
        }
        let link = try encode(source)
        return "<!doctype html>\n<html lang=\"en\"><meta charset=\"utf-8\"><title>Install " + escapeHTML(name) + "</title>\n"
            + "<h1>Install " + escapeHTML(name) + "</h1>\n"
            + "<p>Review the source, then drag this link to your bookmarks bar. Open the target page and click the saved bookmark.</p>\n"
            + "<p><a id=\"bookmarklet\" draggable=\"true\" href=\"" + escapeHTML(link) + "\">" + escapeHTML(name) + "</a></p>\n"
            + "<p>No extension process is needed. The script runs with the target page's access. Browser restrictions or the page's content security policy may prevent execution.</p>\n"
            + "<h2>Source</h2><pre>" + escapeHTML(source) + "</pre></html>\n"
    }

    private static let wrapperStart = "void (function(){\n"
    private static let wrapperEnd = "\n}).call(window);"

    private static func asciiLower(_ byte: UInt8) -> UInt8 {
        byte >= 0x41 && byte <= 0x5A ? byte + 0x20 : byte
    }

    /// html.EscapeString.
    private static func escapeHTML(_ text: String) -> String {
        var out = ""
        for scalar in text.unicodeScalars {
            switch scalar {
            case "&": out += "&amp;"
            case "<": out += "&lt;"
            case ">": out += "&gt;"
            case "\"": out += "&#34;"
            case "'": out += "&#39;"
            default: out.unicodeScalars.append(scalar)
            }
        }
        return out
    }
}
