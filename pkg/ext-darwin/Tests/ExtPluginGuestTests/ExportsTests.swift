import CExtEngine
import ExtPluginExports
import ExtPluginGuest
import XCTest

// The test bundle plays the part of a plugin: it defines the factory and then
// drives the exports through the C-ABI signatures of ext_plugin.h.
@_cdecl("ext_plugin_guest_factory")
public func ext_plugin_guest_factory() -> UnsafeMutableRawPointer? {
    exportGuest {
        try Guest(
            descriptor: Descriptor(
                identity: Identity(id: "swift/exports", revision: "r1"),
                contracts: [Contract(name: "test", version: "v1", operations: [Operation(name: "echo")])]),
            handler: { $0.payload })
    }
}

final class ExportsTests: XCTestCase {
    private let frame = Int(extPluginMaxFrameBytes)

    private func call(_ handle: UInt64, _ operation: UInt32, _ json: String,
                      capacity: Int? = nil) -> (status: UInt32, body: String) {
        var response = [UInt8](repeating: 0, count: capacity ?? frame)
        var written: UInt32 = 0
        let request = Array(json.utf8)
        let status = ext_plugin_call(handle, operation, request, UInt32(request.count), &response, UInt32(response.count), &written)
        return (status, String(decoding: response.prefix(Int(written)), as: UTF8.self))
    }

    private let echo = #"{"apiVersion":"ext.plugin/v1","id":"1","plugin":{"id":"swift/exports","revision":"r1"},"contract":{"name":"test","version":"v1"},"operation":"echo","deadline":"2099-01-01T00:00:00Z","payload":{"v":7}}"#

    func testABIVersion() {
        XCTAssertEqual(ext_plugin_abi_version(), UInt32(EXT_PLUGIN_ABI_VERSION))
    }

    func testHandshakeThenInvoke() {
        let handle = ext_plugin_open()
        XCTAssertNotEqual(handle, 0)
        defer { ext_plugin_close(handle) }
        let hello = call(handle, UInt32(EXT_PLUGIN_HANDSHAKE), #"{"deadline":"2099-01-01T00:00:00Z"}"#)
        XCTAssertEqual(hello.status, UInt32(EXT_PLUGIN_OK))
        XCTAssertTrue(hello.body.contains(#""id":"swift/exports""#), hello.body)
        let reply = call(handle, UInt32(EXT_PLUGIN_INVOKE), echo)
        XCTAssertEqual(reply.status, UInt32(EXT_PLUGIN_OK))
        XCTAssertTrue(reply.body.contains(#""payload":{"v":7}"#), reply.body)
    }

    func testInvokeBeforeHandshakeFails() {
        let handle = ext_plugin_open()
        defer { ext_plugin_close(handle) }
        let reply = call(handle, UInt32(EXT_PLUGIN_INVOKE), echo)
        XCTAssertEqual(reply.status, UInt32(EXT_PLUGIN_FAILED))
        XCTAssertEqual(reply.body, "")
    }

    func testExpiredHandshakeFails() {
        let handle = ext_plugin_open()
        defer { ext_plugin_close(handle) }
        XCTAssertEqual(call(handle, UInt32(EXT_PLUGIN_HANDSHAKE), #"{"deadline":"2001-01-01T00:00:00Z"}"#).status, UInt32(EXT_PLUGIN_FAILED))
    }

    func testClosedAndUnknownHandles() {
        let handle = ext_plugin_open()
        ext_plugin_close(handle)
        XCTAssertEqual(call(handle, UInt32(EXT_PLUGIN_HANDSHAKE), #"{"deadline":"2099-01-01T00:00:00Z"}"#).status, UInt32(EXT_PLUGIN_CLOSED))
        XCTAssertEqual(call(999_999, UInt32(EXT_PLUGIN_HANDSHAKE), "{}").status, UInt32(EXT_PLUGIN_CLOSED))
    }

    func testBadCapacityAndEmptyRequestAreInvalid() {
        let handle = ext_plugin_open()
        defer { ext_plugin_close(handle) }
        XCTAssertEqual(call(handle, UInt32(EXT_PLUGIN_HANDSHAKE), "{}", capacity: 1024).status, UInt32(EXT_PLUGIN_INVALID))
        XCTAssertEqual(call(handle, UInt32(EXT_PLUGIN_HANDSHAKE), "").status, UInt32(EXT_PLUGIN_INVALID))
    }

    func testSessionsAreIndependent() {
        let a = ext_plugin_open(), b = ext_plugin_open()
        defer { ext_plugin_close(a); ext_plugin_close(b) }
        XCTAssertNotEqual(a, b)
        XCTAssertEqual(call(a, UInt32(EXT_PLUGIN_HANDSHAKE), #"{"deadline":"2099-01-01T00:00:00Z"}"#).status, UInt32(EXT_PLUGIN_OK))
        XCTAssertEqual(call(b, UInt32(EXT_PLUGIN_INVOKE), echo).status, UInt32(EXT_PLUGIN_FAILED))
    }
}
