import Foundation

#if canImport(UIKit)
import UIKit
#endif

/// Bounds for one keyboard-extension editor session.
public struct KeyboardLimits: Equatable, Sendable {
    public static let maxSelectionCharacters = 20_000
    public static let maxInsertCharacters = 10_000

    public let maxSelectionCharacters: Int
    public let maxInsertCharacters: Int

    public static let standard = try! KeyboardLimits()

    public init(maxSelectionCharacters: Int = 20_000, maxInsertCharacters: Int = 10_000) throws {
        guard (1...Self.maxSelectionCharacters).contains(maxSelectionCharacters),
              (1...Self.maxInsertCharacters).contains(maxInsertCharacters) else {
            throw KeyboardError.invalidValue
        }
        self.maxSelectionCharacters = maxSelectionCharacters
        self.maxInsertCharacters = maxInsertCharacters
    }
}

public enum KeyboardError: Error, Equatable {
    case invalidValue
    case limitExceeded
    case unavailable
}

/// The text-document operations a keyboard extension can perform. UIKit's
/// `UITextDocumentProxy` conforms; a test or another host can supply its own.
public protocol KeyboardTextProxy: AnyObject {
    var keyboardSelectedText: String? { get }
    var keyboardContextBefore: String? { get }
    var keyboardContextAfter: String? { get }
    func insertKeyboardText(_ text: String)
    func deleteKeyboardBackward()
    func deleteKeyboardForward()
}

/// Switching input modes stays with the native keyboard host. The editor never
/// chooses a keyboard or reads the installed keyboard list.
public protocol KeyboardHost: AnyObject {
    func advanceToNextInputMode()
}

/// Bounded helpers around a keyboard extension's text document and host.
/// Selection is whitespace-trimmed and blank selections report `nil`; oversized
/// selections report `nil` rather than copying unbounded text.
public final class KeyboardEditor {
    private let proxy: KeyboardTextProxy
    private weak var host: KeyboardHost?
    private let limits: KeyboardLimits

    public init(proxy: KeyboardTextProxy, host: KeyboardHost? = nil,
                limits: KeyboardLimits = .standard) {
        self.proxy = proxy
        self.host = host
        self.limits = limits
    }

    public func selection() -> String? {
        guard let value = proxy.keyboardSelectedText else { return nil }
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, trimmed.count <= limits.maxSelectionCharacters else { return nil }
        return trimmed
    }

    public func contextBefore(limit: Int = 300) -> String? {
        Self.tail(proxy.keyboardContextBefore, limit: limit)
    }

    public func contextAfter(limit: Int = 300) -> String? {
        Self.head(proxy.keyboardContextAfter, limit: limit)
    }

    /// Inserts already-reviewed text. Empty text, NUL, or text beyond the insert
    /// bound fails instead of truncating silently.
    @discardableResult
    public func insert(_ text: String) throws -> Bool {
        guard !text.isEmpty, !text.contains("\0") else { throw KeyboardError.invalidValue }
        guard text.count <= limits.maxInsertCharacters else { throw KeyboardError.limitExceeded }
        proxy.insertKeyboardText(text)
        return true
    }

    /// Submits the editor's default action by inserting a newline. The caller
    /// decides whether that is a send, a search, or a line break.
    @discardableResult
    public func submit() throws -> Bool {
        try insert("\n")
    }

    public func deleteBackward(_ count: Int = 1) throws {
        try validateDeleteCount(count)
        for _ in 0..<count { proxy.deleteKeyboardBackward() }
    }

    public func deleteForward(_ count: Int = 1) throws {
        try validateDeleteCount(count)
        for _ in 0..<count { proxy.deleteKeyboardForward() }
    }

    /// Advances to the next installed keyboard when the host offers one. It
    /// does not open a picker or change application settings.
    public func nextKeyboard() -> Bool {
        guard let host else { return false }
        host.advanceToNextInputMode()
        return true
    }

    public var canSwitchKeyboard: Bool { host != nil }

    private func validateDeleteCount(_ count: Int) throws {
        guard count >= 0 else { throw KeyboardError.invalidValue }
        guard count <= limits.maxInsertCharacters else { throw KeyboardError.limitExceeded }
    }

    static func tail(_ value: String?, limit: Int) -> String? {
        guard let value, limit > 0, !value.isEmpty else { return nil }
        guard value.count > limit else { return value }
        return String(value.suffix(limit))
    }

    static func head(_ value: String?, limit: Int) -> String? {
        guard let value, limit > 0, !value.isEmpty else { return nil }
        guard value.count > limit else { return value }
        return String(value.prefix(limit))
    }
}

#if canImport(UIKit)
extension UITextDocumentProxy {
    public var keyboardSelectedText: String? { selectedText }
    public var keyboardContextBefore: String? { documentContextBeforeInput }
    public var keyboardContextAfter: String? { documentContextAfterInput }

    public func insertKeyboardText(_ text: String) { insertText(text) }

    public func deleteKeyboardBackward() { deleteBackward() }

    public func deleteKeyboardForward() {
        adjustTextPosition(byCharacterOffset: 1)
        deleteBackward()
    }
}

#if os(iOS)
extension UIInputViewController: KeyboardHost {}
#endif
#endif
