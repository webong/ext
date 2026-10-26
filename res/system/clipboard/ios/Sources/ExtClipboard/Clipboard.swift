import Foundation

#if os(macOS)
import AppKit
#elseif os(iOS)
import UIKit
#endif

public let clipboardMaximumPolicyBytes = 16 * 1024 * 1024
public let clipboardMaximumItems = 64
public let clipboardMaximumFormats = 32

public enum ClipboardError: Error, Equatable {
    case invalidValue
    case denied
    case limitExceeded
    case changed
    case nativeFailure
}

public struct ClipboardPolicy: Codable, Equatable, Sendable {
    public let maxBytes: Int
    public let allowedFormats: [String]
    public let allowRead: Bool
    public let allowWrite: Bool

    public init(maxBytes: Int, allowedFormats: [String], allowRead: Bool = false, allowWrite: Bool = false) {
        self.maxBytes = maxBytes
        self.allowedFormats = allowedFormats
        self.allowRead = allowRead
        self.allowWrite = allowWrite
    }

    public func validate() throws {
        guard maxBytes > 0, maxBytes <= clipboardMaximumPolicyBytes,
              (1...clipboardMaximumFormats).contains(allowedFormats.count),
              Set(allowedFormats).count == allowedFormats.count,
              allowedFormats.allSatisfy({ !$0.isEmpty && $0.utf8.count <= 256 }) else {
            throw ClipboardError.invalidValue
        }
    }
}

public struct ClipboardRepresentation: Codable, Equatable, Sendable {
    public let format: String
    public let data: Data
    public init(format: String, data: Data) { self.format = format; self.data = data }
}

public struct ClipboardItem: Codable, Equatable, Sendable {
    public let representations: [ClipboardRepresentation]
    public init(representations: [ClipboardRepresentation]) { self.representations = representations }
}

public struct ClipboardSnapshot: Codable, Equatable, Sendable {
    public let generation: String
    public let items: [ClipboardItem]
    public init(generation: String, items: [ClipboardItem]) { self.generation = generation; self.items = items }
}

public struct ClipboardReceipt: Codable, Equatable, Sendable {
    public let generation: String
    public let dispatch: String
    public init(generation: String, dispatch: String) { self.generation = generation; self.dispatch = dispatch }
}

public enum ClipboardValidation {
    public static func validate(items: [ClipboardItem], maxBytes: Int, formats: [String]) throws {
        guard maxBytes > 0, maxBytes <= clipboardMaximumPolicyBytes, items.count <= clipboardMaximumItems else {
            throw ClipboardError.limitExceeded
        }
        let allowed = Set(formats)
        var bytes = 0
        for item in items {
            guard (1...clipboardMaximumFormats).contains(item.representations.count) else {
                throw ClipboardError.invalidValue
            }
            var seen = Set<String>()
            for representation in item.representations {
                guard allowed.contains(representation.format), seen.insert(representation.format).inserted else {
                    throw ClipboardError.denied
                }
                bytes += representation.data.count
                guard bytes <= maxBytes else { throw ClipboardError.limitExceeded }
            }
        }
    }
}

/// A caller-owned binding to this application's native system clipboard.
/// Access is main-actor serialized. It performs no permission prompting,
/// approval UI, transport, or automatic restoration.
@MainActor
public final class SystemClipboard {
    private let policy: ClipboardPolicy
    private let producerID = UUID().uuidString.lowercased()

    public init(policy: ClipboardPolicy) throws {
        try policy.validate()
        self.policy = policy
    }

    /// Returns mutation metadata without reading clipboard contents. This is
    /// available to write-only policies so they can make a conditional write.
    public func currentGeneration() throws -> String {
#if os(macOS)
        return generation(NSPasteboard.general)
#elseif os(iOS)
        return generation(UIPasteboard.general)
#else
        throw ClipboardError.nativeFailure
#endif
    }

    public func read() throws -> ClipboardSnapshot {
        guard policy.allowRead else { throw ClipboardError.denied }
#if os(macOS)
        return try readMacOS()
#elseif os(iOS)
        return try readIOS()
#else
        throw ClipboardError.nativeFailure
#endif
    }

    public func write(expectedGeneration: String, items: [ClipboardItem]) throws -> ClipboardReceipt {
        guard policy.allowWrite else { throw ClipboardError.denied }
        guard !expectedGeneration.isEmpty else { throw ClipboardError.invalidValue }
        try ClipboardValidation.validate(items: items, maxBytes: policy.maxBytes, formats: policy.allowedFormats)
#if os(macOS)
        return try writeMacOS(expectedGeneration: expectedGeneration, items: items)
#elseif os(iOS)
        return try writeIOS(expectedGeneration: expectedGeneration, items: items)
#else
        throw ClipboardError.nativeFailure
#endif
    }

    public func clear(expectedGeneration: String) throws -> ClipboardReceipt {
        try write(expectedGeneration: expectedGeneration, items: [])
    }

#if os(macOS)
    private func generation(_ board: NSPasteboard) -> String { "\(producerID):\(board.changeCount)" }

    private func readMacOS() throws -> ClipboardSnapshot {
        let board = NSPasteboard.general
        let before = generation(board)
        let nativeItems = board.pasteboardItems ?? []
        guard nativeItems.count <= clipboardMaximumItems else { throw ClipboardError.limitExceeded }
        var items: [ClipboardItem] = []
        for nativeItem in nativeItems {
            guard nativeItem.types.count <= clipboardMaximumFormats else { throw ClipboardError.limitExceeded }
            var representations: [ClipboardRepresentation] = []
            for type in nativeItem.types {
                let format = type.rawValue
                guard policy.allowedFormats.contains(format) else { throw ClipboardError.denied }
                guard let data = nativeItem.data(forType: type) else { throw ClipboardError.nativeFailure }
                representations.append(ClipboardRepresentation(format: format, data: data))
            }
            items.append(ClipboardItem(representations: representations))
        }
        try ClipboardValidation.validate(items: items, maxBytes: policy.maxBytes, formats: policy.allowedFormats)
        guard generation(board) == before else { throw ClipboardError.changed }
        return ClipboardSnapshot(generation: before, items: items)
    }

    private func writeMacOS(expectedGeneration: String, items: [ClipboardItem]) throws -> ClipboardReceipt {
        let board = NSPasteboard.general
        guard generation(board) == expectedGeneration else { throw ClipboardError.changed }
        var nativeItems: [NSPasteboardItem] = []
        for item in items {
            let nativeItem = NSPasteboardItem()
            for representation in item.representations {
                guard nativeItem.setData(representation.data, forType: NSPasteboard.PasteboardType(representation.format)) else {
                    throw ClipboardError.nativeFailure
                }
            }
            nativeItems.append(nativeItem)
        }
        board.clearContents()
        if !nativeItems.isEmpty, !board.writeObjects(nativeItems) { throw ClipboardError.nativeFailure }
        return ClipboardReceipt(generation: generation(board), dispatch: "dispatched")
    }
#elseif os(iOS)
    private func generation(_ board: UIPasteboard) -> String { "\(producerID):\(board.changeCount)" }

    private func readIOS() throws -> ClipboardSnapshot {
        let board = UIPasteboard.general
        let before = generation(board)
        let nativeItems = board.items
        guard nativeItems.count <= clipboardMaximumItems else { throw ClipboardError.limitExceeded }
        var items: [ClipboardItem] = []
        for nativeItem in nativeItems {
            guard nativeItem.count <= clipboardMaximumFormats else { throw ClipboardError.limitExceeded }
            var representations: [ClipboardRepresentation] = []
            for (format, value) in nativeItem {
                guard policy.allowedFormats.contains(format) else { throw ClipboardError.denied }
                let data: Data
                if let value = value as? Data { data = value }
                else if let value = value as? String { data = Data(value.utf8) }
                else if let value = value as? URL { data = Data(value.absoluteString.utf8) }
                else { throw ClipboardError.nativeFailure }
                representations.append(ClipboardRepresentation(format: format, data: data))
            }
            items.append(ClipboardItem(representations: representations.sorted { $0.format < $1.format }))
        }
        try ClipboardValidation.validate(items: items, maxBytes: policy.maxBytes, formats: policy.allowedFormats)
        guard generation(board) == before else { throw ClipboardError.changed }
        return ClipboardSnapshot(generation: before, items: items)
    }

    private func writeIOS(expectedGeneration: String, items: [ClipboardItem]) throws -> ClipboardReceipt {
        let board = UIPasteboard.general
        guard generation(board) == expectedGeneration else { throw ClipboardError.changed }
        let nativeItems: [[String: Any]] = items.map { item in
            Dictionary(uniqueKeysWithValues: item.representations.map { ($0.format, $0.data as Any) })
        }
        board.setItems(nativeItems, options: [:])
        return ClipboardReceipt(generation: generation(board), dispatch: "dispatched")
    }
#endif
}
