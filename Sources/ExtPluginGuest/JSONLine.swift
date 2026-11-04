import CExtEngine
import Foundation

/// The ext.plugin/v1 JSON-line transport for a guest process: one JSON frame per line, a
/// `plugin.hello` frame first, then one response per request. Standard output belongs to the
/// protocol; log to standard error.
///
/// The input is read on a thread of its own, so a handler can report progress (`Call.progress`)
/// and call its host (`Call.reverse`) and still have the host's answers read. A frame the host
/// may not send, such as a second request while a call is in flight, ends the session with a
/// `JSONLineError` and the call in flight is not answered. When the input ends, the call in
/// flight is answered and the session ends.
///
/// A guest exported through the `ext_plugin_*` C ABI, one run by an app's own transport and one
/// called with `Guest.invoke` have no frame channel; on them progress is `unsupported` and a call
/// has no reverse caller. This transport is for a guest process, so on iOS (which cannot spawn
/// one) it is for tests and for an app that runs the session over its own pipes.
public enum JSONLine {
    private static let helloFields: Set<String> = ["apiVersion", "id", "operation", "deadline", "plugin", "contract", "surface"]

    /// Serves the guest on standard input and output until the host closes the pipe.
    public static func serveStandardIO(_ guest: Guest) throws {
        signal(SIGPIPE, SIG_IGN)
        try serve(guest, input: STDIN_FILENO, output: STDOUT_FILENO)
    }

    /// Serves until end of input. A malformed frame ends the session with an error and writes
    /// nothing, so the host sees a failed process.
    public static func serve(_ guest: Guest, input: Int32, output: Int32) throws {
        let connection = Connection(declared: guest.declared, output: output)
        connection.start(input: input)
        defer { connection.end(nil, violation: false) }
        var hello = false
        while let frame = connection.next() {
            var response: [UInt8]
            if !hello {
                try checkHello(frame)
                hello = true
                response = Array(#"{"apiVersion":"ext.plugin/v1","id":"hello","payload":"#.utf8)
                response += Array(try guest.descriptorJSON())
                response.append(UInt8(ascii: "}"))
            } else {
                response = Array(try guest.invoke(Data(frame), timeoutMilliseconds: Guest.defaultMaxCallMilliseconds, connection: connection))
            }
            // A violation found while the handler ran ends the session, and the call goes unanswered.
            if connection.violated { break }
            connection.idle()  // before the response, so the next request is never early
            try connection.write(response)
        }
        if let failure = connection.failure { throw failure }
    }

    private static func checkHello(_ frame: [UInt8]) throws {
        let bad = JSONLineError.io("invalid hello")
        guard (try? Frames.decode(frame)) != nil, let hello = (try? Frames.decode(frame))?.object,
            Set(hello.keys).isSubset(of: helloFields), hello["apiVersion"]?.string == apiVersion, hello["id"]?.string == "hello",
            hello["operation"]?.string == "plugin.hello", let deadline = hello["deadline"]?.string,
            emptyObject(hello["plugin"]), emptyObject(hello["contract"]),
            hello["surface"] == nil || hello["surface"]?.isNull == true || hello["surface"]?.string == ""
        else { throw bad }
        guard let when = try? Frames.instant(deadline) else { throw JSONLineError.io("invalid hello deadline") }
        if when <= Connection.now() { throw JSONLineError.io("hello deadline passed") }
    }

    /// An object (or nothing) whose every member is the empty string.
    private static func emptyObject(_ value: JSONValue?) -> Bool {
        guard let value, !value.isNull else { return true }
        guard let members = value.object else { return false }
        return members.values.allSatisfy { $0.string == "" }
    }
}
