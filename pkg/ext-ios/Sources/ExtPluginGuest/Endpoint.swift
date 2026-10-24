import CExtEngine
import Foundation

/// Why the engine did not admit an endpoint. An outcome is a status and never
/// text, because an endpoint is a credential: nothing of it reaches an error.
public enum EndpointError: Error, Equatable {
    /// Malformed: unknown or duplicate fields, a scheme, address, path, token or
    /// expiry the rules refuse, or an address that is not loopback.
    case invalid
    /// Well formed, but the policy does not admit it (scheme, lifetime).
    case denied
    /// Its expiry has already passed.
    case expired
    /// Any other engine status.
    case engine(Int32)
}

/// Loopback data endpoints: the engine's rules for the endpoint a host hands a
/// plugin (`endpoint.check` and `endpoint.validate`, see lib/plugin/services.md).
/// A plugin checks what it receives before dialing; a host validates what it
/// grants. Granting one (its lifetime, one-time tokens, dialing) stays with the caller.
public enum Endpoint {
    /// Checks the shape rules alone: strict decoding, scheme, loopback address,
    /// path, token and expiry. `endpoint` is the endpoint as JSON.
    public static func check(_ endpoint: Data) throws {
        try call("endpoint.check", endpoint)
    }

    /// Checks the shape rules and the policy, the JSON of
    /// `{"schemes":[...],"maxLifetimeMilliseconds":n,"now":"RFC 3339"}` with every
    /// field optional (a policy that names no scheme admits nothing).
    public static func validate(_ endpoint: Data, policy: Data) throws {
        var input = Data(#"{"endpoint":"#.utf8)
        input.append(endpoint)
        input.append(Data(#","policy":"#.utf8))
        input.append(policy)
        input.append(UInt8(ascii: "}"))
        try call("endpoint.validate", input)
    }

    private static func call(_ operation: String, _ input: Data) throws {
        var out = ext_buffer(data: nil, len: 0)
        defer { ext_buffer_free(&out) }
        let status: ext_status = input.withUnsafeBytes { raw in
            ext_engine_call(operation, raw.bindMemory(to: UInt8.self).baseAddress, raw.count, &out)
        }
        switch status {
        case ext_status(EXT_OK): return
        case ext_status(EXT_INVALID): throw EndpointError.invalid
        case ext_status(EXT_DENIED): throw EndpointError.denied
        case ext_status(EXT_EXPIRED): throw EndpointError.expired
        default: throw EndpointError.engine(status)
        }
    }
}
