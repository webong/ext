import Foundation

#if os(macOS)
import AppKit

public enum AppleEventValue: Equatable, Sendable {
    case string(String)
    case boolean(Bool)
    case integer(Int32)
    case number(Double)
    case none
}

public struct AppleEventParameter: Equatable, Sendable {
    public let keyword: String
    public let value: AppleEventValue

    public init(keyword: String, value: AppleEventValue) {
        self.keyword = keyword
        self.value = value
    }
}

public enum SystemAppleEventError: Error, Equatable {
    case invalidInput
    case targetUnavailable
    case nativeFailure
}

/// Sends one typed Apple Event to a caller-selected running application. Event
/// mappings, allowlists and approval policy remain with the consumer or owning
/// product adapter.
public enum SystemAppleEvent {
    public static func send(
        bundleIdentifier: String,
        eventClass: String,
        eventID: String,
        parameters: [AppleEventParameter] = [],
        timeout: TimeInterval = 30
    ) throws -> AppleEventValue {
        guard !bundleIdentifier.isEmpty, bundleIdentifier.utf8.count <= 1_024,
              (0...300).contains(timeout), parameters.count <= 64 else {
            throw SystemAppleEventError.invalidInput
        }
        guard !NSRunningApplication.runningApplications(withBundleIdentifier: bundleIdentifier).isEmpty else {
            throw SystemAppleEventError.targetUnavailable
        }
        let target = NSAppleEventDescriptor(bundleIdentifier: bundleIdentifier)
        let event = NSAppleEventDescriptor(
            eventClass: try fourCharacterCode(eventClass),
            eventID: try fourCharacterCode(eventID),
            targetDescriptor: target,
            returnID: AEReturnID(kAutoGenerateReturnID),
            transactionID: AETransactionID(kAnyTransactionID)
        )
        var keywords = Set<String>()
        for parameter in parameters {
            guard keywords.insert(parameter.keyword).inserted else {
                throw SystemAppleEventError.invalidInput
            }
            event.setParam(try descriptor(parameter.value),
                           forKeyword: try fourCharacterCode(parameter.keyword))
        }
        do {
            let reply = try event.sendEvent(options: [.waitForReply], timeout: timeout)
            return decode(reply.paramDescriptor(forKeyword: keyDirectObject))
        } catch {
            throw SystemAppleEventError.nativeFailure
        }
    }

    static func fourCharacterCode(_ value: String) throws -> OSType {
        let bytes = Array(value.utf8)
        guard bytes.count == 4, bytes.allSatisfy({ $0 < 128 }) else {
            throw SystemAppleEventError.invalidInput
        }
        return bytes.reduce(OSType(0)) { ($0 << 8) | OSType($1) }
    }

    private static func descriptor(_ value: AppleEventValue) throws -> NSAppleEventDescriptor {
        switch value {
        case .string(let text):
            guard text.utf8.count <= 1_048_576 else { throw SystemAppleEventError.invalidInput }
            return NSAppleEventDescriptor(string: text)
        case .boolean(let boolean):
            return NSAppleEventDescriptor(boolean: boolean)
        case .integer(let integer):
            return NSAppleEventDescriptor(int32: integer)
        case .number(let number):
            guard number.isFinite else { throw SystemAppleEventError.invalidInput }
            return NSAppleEventDescriptor(double: number)
        case .none:
            throw SystemAppleEventError.invalidInput
        }
    }

    private static func decode(_ descriptor: NSAppleEventDescriptor?) -> AppleEventValue {
        guard let descriptor else { return .none }
        switch descriptor.descriptorType {
        case typeBoolean:
            return .boolean(descriptor.booleanValue)
        case typeSInt16, typeSInt32, typeSInt64, typeUInt32:
            return .integer(descriptor.int32Value)
        case typeIEEE32BitFloatingPoint, typeIEEE64BitFloatingPoint:
            return .number(descriptor.doubleValue)
        default:
            return descriptor.stringValue.map(AppleEventValue.string) ?? .none
        }
    }
}
#endif
