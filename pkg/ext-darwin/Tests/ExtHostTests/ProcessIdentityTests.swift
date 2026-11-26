import ExtHost
import Foundation
import XCTest

#if os(macOS)
final class ProcessIdentityTests: XCTestCase {
    func testCurrentProcessHasLedgerBirthToken() throws {
        let token = try LedgerProcessIdentity.startID(pid: Int(ProcessInfo.processInfo.processIdentifier))
        XCTAssertFalse(token.isEmpty)
    }
}
#endif
