import XCTest
@testable import ExtSystem

#if os(macOS)
import ApplicationServices

final class AccessibilityControlTests: XCTestCase {
    private func element() -> AccessibilityElement {
        AccessibilityElement(
            id: "accessibility:opaque", parentID: nil, pid: 42, processStartID: "42:1",
            role: "textfield", identifier: "name",
            bounds: SurfaceBounds(x: 1, y: 2, width: 30, height: 40), actions: []
        )
    }

    @MainActor
    func testReadRejectsMalformedAttributeNamesBeforeTouchingTheApplication() {
        let controller = SystemAccessibilityController()
        for name in ["", "role", "AXRole\nAXValue", "AX" + String(repeating: "x", count: 200)] {
            XCTAssertThrowsError(try controller.read(element: element(), attribute: name)) {
                XCTAssertEqual($0 as? AccessibilityControlError, .invalidInput, name)
            }
        }
    }

    func testPlainDataAttributeValuesAreDecoded() throws {
        XCTAssertEqual(try decodeAccessibilityAttribute("name" as CFString), .string("name"))
        XCTAssertEqual(try decodeAccessibilityAttribute(kCFBooleanTrue), .boolean(true))
        XCTAssertEqual(try decodeAccessibilityAttribute(7.5 as CFNumber), .number(7.5))
        var point = CGPoint(x: 12, y: -3)
        XCTAssertEqual(try decodeAccessibilityAttribute(AXValueCreate(.cgPoint, &point)!), .point(x: 12, y: -3))
        var size = CGSize(width: 640, height: 480)
        XCTAssertEqual(try decodeAccessibilityAttribute(AXValueCreate(.cgSize, &size)!), .size(width: 640, height: 480))
        var range = CFRange(location: 4, length: 9)
        XCTAssertEqual(try decodeAccessibilityAttribute(AXValueCreate(.cfRange, &range)!), .range(location: 4, length: 9))
    }

    func testValuesThatAreNotPlainDataAreRefused() {
        let url = URL(string: "https://example.invalid/")! as CFURL
        XCTAssertThrowsError(try decodeAccessibilityAttribute(url)) {
            XCTAssertEqual($0 as? AccessibilityControlError, .unsupported)
        }
        XCTAssertThrowsError(try decodeAccessibilityAttribute([1, 2] as CFArray)) {
            XCTAssertEqual($0 as? AccessibilityControlError, .unsupported)
        }
        XCTAssertThrowsError(try decodeAccessibilityAttribute(Double.nan as CFNumber)) {
            XCTAssertEqual($0 as? AccessibilityControlError, .unsupported)
        }
    }
}
#endif
