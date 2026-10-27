import Foundation

#if canImport(CryptoKit)
import CryptoKit
#endif

#if canImport(UniformTypeIdentifiers)
import UniformTypeIdentifiers
#endif

/// Bounds fixed by the embedding application before a share extension loads any
/// item. Zero values select the documented defaults; every value has a library
/// ceiling so a caller cannot disable bounding.
public struct ShareLimits: Equatable, Sendable {
    public static let maxItems = 128
    public static let maxBytes = 64 * 1024 * 1024
    public static let maxTextBytes = 1024 * 1024
    public static let maxURLLength = 8192
    public static let maxNameLength = 255
    public static let maxMIMETypeLength = 255

    /// Default bounds suitable for an extension that does not configure its own.
    public static let standard = try! ShareLimits()

    public let maxItems: Int
    public let maxBytes: Int
    public let maxTextBytes: Int
    public let maxURLLength: Int

    public init(maxItems: Int = 32, maxBytes: Int = 16 * 1024 * 1024,
                maxTextBytes: Int = 64 * 1024, maxURLLength: Int = 8192) throws {
        guard (1...Self.maxItems).contains(maxItems),
              (1...Self.maxBytes).contains(maxBytes),
              (1...Self.maxTextBytes).contains(maxTextBytes),
              (1...Self.maxURLLength).contains(maxURLLength) else {
            throw ShareError.invalidValue
        }
        self.maxItems = maxItems
        self.maxBytes = maxBytes
        self.maxTextBytes = maxTextBytes
        self.maxURLLength = maxURLLength
    }
}

public enum ShareError: Error, Equatable {
    case invalidValue
    case limitExceeded
    case loadFailed
    case containerUnavailable
}

/// One shared file. Exactly one of `data` and `fileURL` carries the content;
/// `byteCount` is the content size and must agree with `data`.
public struct ShareAttachment: Codable, Equatable, Sendable {
    public let name: String
    public let mimeType: String
    public let byteCount: Int
    public let data: Data?
    public let fileURL: URL?

    public init(name: String, mimeType: String, byteCount: Int,
                data: Data? = nil, fileURL: URL? = nil) {
        self.name = name
        self.mimeType = mimeType
        self.byteCount = byteCount
        self.data = data
        self.fileURL = fileURL
    }
}

/// One contribution from a native share source. `subject` is optional preview
/// text from the sharing application, never a destination or an approval.
public struct ShareLoadedItem: Equatable, Sendable {
    public var subject: String?
    public var text: String?
    public var urls: [String]
    public var attachments: [ShareAttachment]

    public init(subject: String? = nil, text: String? = nil,
                urls: [String] = [], attachments: [ShareAttachment] = []) {
        self.subject = subject
        self.text = text
        self.urls = urls
        self.attachments = attachments
    }
}

/// The normalized, deduplicated result. The embedding application owns the
/// destination and any further transport.
public struct SharePayload: Codable, Equatable, Sendable {
    public let subject: String?
    public let text: String?
    public let urls: [String]
    public let attachments: [ShareAttachment]
    public let byteCount: Int

    public init(subject: String?, text: String?, urls: [String],
                attachments: [ShareAttachment], byteCount: Int) {
        self.subject = subject
        self.text = text
        self.urls = urls
        self.attachments = attachments
        self.byteCount = byteCount
    }
}

/// A caller-configured destination for copied attachments. The extension
/// container, app group, and directory layout are never chosen here; the
/// application supplies them explicitly.
public protocol ShareAttachmentSink: Sendable {
    /// Copies a readable source into the destination and returns its location.
    /// The source URL is valid only for the duration of the call.
    func storeAttachment(_ source: URL, name: String, mimeType: String, index: Int) throws -> URL
}

/// Stores attachments in a directory of an app group the application names.
public struct AppGroupAttachmentSink: ShareAttachmentSink {
    public let groupIdentifier: String
    public let directoryName: String

    public init(groupIdentifier: String, directoryName: String = "SharedInbox") throws {
        guard !groupIdentifier.isEmpty, groupIdentifier.utf8.count <= 255,
              ShareValidation.isPortableName(directoryName) else {
            throw ShareError.invalidValue
        }
        self.groupIdentifier = groupIdentifier
        self.directoryName = directoryName
    }

    public func storeAttachment(_ source: URL, name: String, mimeType: String, index: Int) throws -> URL {
        guard let container = FileManager.default.containerURL(
            forSecurityApplicationGroupIdentifier: groupIdentifier
        ) else {
            throw ShareError.containerUnavailable
        }
        let directory = container.appendingPathComponent(directoryName, isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let target = directory.appendingPathComponent("\(index)-\(name)")
        if FileManager.default.fileExists(atPath: target.path) {
            try FileManager.default.removeItem(at: target)
        }
        try FileManager.default.copyItem(at: source, to: target)
        return target
    }
}

public enum ShareValidation {
    /// Portable file-name rule shared with the Go and Android contracts.
    public static func isPortableName(_ value: String) -> Bool {
        guard !value.isEmpty, value.utf8.count <= ShareLimits.maxNameLength,
              value != ".", value != "..",
              !value.contains("/"), !value.contains("\\"), !value.contains("\0") else {
            return false
        }
        return value.unicodeScalars.allSatisfy { !CharacterSet.controlCharacters.contains($0) }
    }

    static func text(_ value: String?, limit: Int) throws -> String? {
        guard let value, !value.isEmpty else { return nil }
        guard value.utf8.count <= limit, !value.contains("\0") else {
            throw ShareError.limitExceeded
        }
        return value
    }

    static func link(_ value: String, limit: Int) throws -> String {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, trimmed.utf8.count <= limit, !trimmed.contains("\0"),
              !trimmed.unicodeScalars.contains(where: { CharacterSet.controlCharacters.contains($0) }),
              let url = URL(string: trimmed), let scheme = url.scheme else {
            throw ShareError.invalidValue
        }
        var components = URLComponents(url: url, resolvingAgainstBaseURL: false)
        components?.scheme = scheme.lowercased()
        return components?.url?.absoluteString ?? url.absoluteString
    }

    static func attachment(_ value: ShareAttachment) throws -> ShareAttachment {
        guard isPortableName(value.name) else { throw ShareError.invalidValue }
        let mimeType = value.mimeType.isEmpty ? "application/octet-stream" : value.mimeType
        guard mimeType.utf8.count <= ShareLimits.maxMIMETypeLength, !mimeType.contains("\0"),
              value.data == nil || value.fileURL == nil else {
            throw ShareError.invalidValue
        }
        if let data = value.data {
            guard value.byteCount == 0 || value.byteCount == data.count else {
                throw ShareError.invalidValue
            }
            return ShareAttachment(name: value.name, mimeType: mimeType,
                                   byteCount: data.count, data: data)
        }
        guard value.byteCount >= 0 else { throw ShareError.invalidValue }
        if let fileURL = value.fileURL, !fileURL.isFileURL {
            throw ShareError.invalidValue
        }
        return ShareAttachment(name: value.name, mimeType: mimeType,
                               byteCount: value.byteCount, fileURL: value.fileURL)
    }
}

public enum ShareNormalizer {
    /// Validates and merges already-loaded items. Exact duplicate texts, links,
    /// and attachments collapse to their first occurrence. Two different
    /// attachments with one file name are rejected rather than renamed.
    public static func normalize(_ items: [ShareLoadedItem],
                                 limits: ShareLimits = ShareLimits.standard) throws -> SharePayload {
        guard items.count <= limits.maxItems else { throw ShareError.limitExceeded }
        var subjects: [String] = []
        var texts: [String] = []
        var urls: [String] = []
        var attachments: [ShareAttachment] = []
        var subjectSeen = Set<String>()
        var textSeen = Set<String>()
        var urlSeen = Set<String>()
        var attachmentSeen = Set<String>()
        var attachmentNames = [String: String]()
        var byteCount = 0
        for item in items {
            if let subject = try ShareValidation.text(item.subject, limit: limits.maxTextBytes),
               subjectSeen.insert(subject).inserted {
                subjects.append(subject)
            }
            if let text = try ShareValidation.text(item.text, limit: limits.maxTextBytes),
               textSeen.insert(text).inserted {
                texts.append(text)
            }
            guard urlSeen.count + item.urls.count <= limits.maxItems else { throw ShareError.limitExceeded }
            for raw in item.urls {
                let link = try ShareValidation.link(raw, limit: limits.maxURLLength)
                if urlSeen.insert(link).inserted { urls.append(link) }
            }
            for candidate in item.attachments {
                let attachment = try ShareValidation.attachment(candidate)
                let key = attachmentKey(attachment)
                guard attachmentSeen.insert(key).inserted else { continue }
                let folded = attachment.name.lowercased()
                if let previous = attachmentNames[folded], previous != key {
                    throw ShareError.invalidValue
                }
                attachmentNames[folded] = key
                byteCount += attachment.byteCount
                guard byteCount <= limits.maxBytes else { throw ShareError.limitExceeded }
                attachments.append(attachment)
            }
        }
        let mergedSubject = subjects.isEmpty ? nil : subjects.joined(separator: "\n")
        let mergedText = texts.isEmpty ? nil : texts.joined(separator: "\n")
        guard (mergedSubject?.utf8.count ?? 0) <= limits.maxTextBytes,
              (mergedText?.utf8.count ?? 0) <= limits.maxTextBytes else {
            throw ShareError.limitExceeded
        }
        return SharePayload(subject: mergedSubject, text: mergedText, urls: urls,
                            attachments: attachments, byteCount: byteCount)
    }

    static func attachmentKey(_ attachment: ShareAttachment) -> String {
        let content: String
        if let data = attachment.data {
#if canImport(CryptoKit)
            content = SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
#else
            content = String(data.hashValue)
#endif
        } else if let fileURL = attachment.fileURL {
            content = fileURL.path
        } else {
            content = ""
        }
        return "\(attachment.name.lowercased())\u{0}\(attachment.mimeType)\u{0}\(attachment.byteCount)\u{0}\(content)"
    }
}

/// Normalizes `NSExtensionItem` share input. The loader reads only the standard
/// text, URL, and file representations and copies files through the caller's
/// sink. It never launches the application, records an event, or chooses a
/// container.
public enum ShareExtensionLoader {
    public static func load(items: [NSExtensionItem],
                            limits: ShareLimits = ShareLimits.standard,
                            sink: ShareAttachmentSink? = nil) async -> Result<SharePayload, ShareError> {
        let loaded = await loadedItems(items: items, limits: limits, sink: sink)
        do {
            return .success(try ShareNormalizer.normalize(loaded, limits: limits))
        } catch let error as ShareError {
            return .failure(error)
        } catch {
            return .failure(.loadFailed)
        }
    }

    static func loadedItems(items: [NSExtensionItem], limits: ShareLimits,
                            sink: ShareAttachmentSink?) async -> [ShareLoadedItem] {
        var result: [ShareLoadedItem] = []
        var attachmentIndex = 0
        for item in items.prefix(limits.maxItems) {
            var loaded = ShareLoadedItem(
                subject: item.attributedContentText?.string,
                text: nil, urls: [], attachments: []
            )
            for provider in item.attachments ?? [] where attachmentIndex < limits.maxItems {
                if provider.hasItemConformingToTypeIdentifier("public.plain-text") {
                    if let value = await loadItem(provider, typeIdentifier: "public.plain-text") {
                        let text: String?
                        if let value = value as? String {
                            text = value
                        } else if let attributed = value as? NSAttributedString {
                            text = attributed.string
                        } else {
                            text = nil
                        }
                        if let text {
                            loaded.text = [loaded.text, text].compactMap { $0 }.joined(separator: "\n")
                        }
                    }
                } else if provider.hasItemConformingToTypeIdentifier("public.url") {
                    if let url = await loadItem(provider, typeIdentifier: "public.url") {
                        if let value = url as? URL {
                            loaded.urls.append(value.absoluteString)
                        } else if let text = url as? String {
                            loaded.urls.append(text)
                        }
                    }
                } else if let identifier = provider.registeredTypeIdentifiers.first {
                    guard let sink else { continue }
                    guard let attachment = await loadAttachment(
                        provider, typeIdentifier: identifier,
                        name: provider.suggestedName, index: attachmentIndex,
                        sink: sink
                    ) else { continue }
                    attachmentIndex += 1
                    loaded.attachments.append(attachment)
                }
            }
            result.append(loaded)
        }
        return result
    }

    static func loadItem(_ provider: NSItemProvider, typeIdentifier: String) async -> NSSecureCoding? {
        await withCheckedContinuation { continuation in
            provider.loadItem(forTypeIdentifier: typeIdentifier, options: nil) { value, _ in
                continuation.resume(returning: value)
            }
        }
    }

    static func loadAttachment(_ provider: NSItemProvider, typeIdentifier: String,
                               name: String?, index: Int,
                               sink: ShareAttachmentSink) async -> ShareAttachment? {
        await withCheckedContinuation { continuation in
            provider.loadFileRepresentation(forTypeIdentifier: typeIdentifier) { source, _ in
                guard let source else {
                    continuation.resume(returning: nil)
                    return
                }
                let preferred = name ?? source.lastPathComponent
                let portable = preferred
                    .replacingOccurrences(of: "/", with: "_")
                    .replacingOccurrences(of: "\\", with: "_")
                guard ShareValidation.isPortableName(portable),
                      let target = try? sink.storeAttachment(
                        source, name: portable,
                        mimeType: mimeType(for: typeIdentifier), index: index
                      ) else {
                    continuation.resume(returning: nil)
                    return
                }
                let size = (try? target.resourceValues(forKeys: [.fileSizeKey]).fileSize) ?? 0
                continuation.resume(returning: ShareAttachment(
                    name: portable, mimeType: mimeType(for: typeIdentifier),
                    byteCount: size, fileURL: target
                ))
            }
        }
    }

    static func mimeType(for identifier: String) -> String {
#if canImport(UniformTypeIdentifiers)
        if #available(iOS 14.0, macOS 11.0, *) {
            if let type = UTType(identifier), let mime = type.preferredMIMEType {
                return mime
            }
        }
#endif
        switch identifier {
        case "public.jpeg": return "image/jpeg"
        case "public.png": return "image/png"
        case "public.heic": return "image/heic"
        case "com.compuserve.gif": return "image/gif"
        case "public.mp3": return "audio/mpeg"
        case "public.mpeg-4": return "video/mp4"
        case "com.adobe.pdf": return "application/pdf"
        case "public.zip-archive": return "application/zip"
        default: return "application/octet-stream"
        }
    }
}
