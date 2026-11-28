import XCTest
import ExtWeb
final class BundlePolicyTests: XCTestCase {
    func testSharedPolicyFixtures() throws {
        struct Vector: Decodable { let name: String, allowNet: Bool, allowOrigins: [String], csp: String }
        struct Vectors: Decodable { let cases: [Vector] }
        let vectors = try Fixtures.load("bundle/policy.json", as: Vectors.self)
        XCTAssertFalse(vectors.cases.isEmpty)
        for v in vectors.cases {
            XCTAssertEqual(BundlePolicy(allowNet: v.allowNet, allowOrigins: v.allowOrigins).contentSecurityPolicy, v.csp, v.name)
        }
    }
}
