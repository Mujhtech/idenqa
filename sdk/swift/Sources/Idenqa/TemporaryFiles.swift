import Foundation

/// Opaque handle to one staged temporary evidence file.
public struct CaptureTemporaryFile: Equatable, Sendable {
    public let reference: String
    public let byteCount: Int
    public let createdAt: Date

    public init(reference: String, byteCount: Int, createdAt: Date) {
        self.reference = reference
        self.byteCount = byteCount
        self.createdAt = createdAt
    }
}

/// Bounded, app-private staging storage for in-flight evidence.
///
/// Implementations must keep files inside app-private storage, enforce the
/// configured bounds, never write evidence bytes to logs, and remove staged
/// files deterministically. Raw evidence is never persisted across process
/// death: staging only covers an in-flight attempt.
public protocol CaptureTemporaryFileStore: Sendable {
    func stage(_ artifact: CapturedArtifact, category: String, now: Date) async throws -> CaptureTemporaryFile
    func data(for file: CaptureTemporaryFile) async throws -> Data
    func remove(_ file: CaptureTemporaryFile) async throws
    func purge(olderThan: Date) async throws -> Int
    func purgeAll() async throws -> Int
    func count() async throws -> Int
    func totalBytes() async throws -> Int
}

/// FileManager-backed app-private staging store.
///
/// On iOS staged files use complete file protection where the platform
/// supports it. On every platform the directory is created inside the app's
/// private caches container with owner-only permissions.
public actor FileManagerCaptureTemporaryFileStore: CaptureTemporaryFileStore {
    private let directory: URL
    private let maximumFileBytes: Int
    private let maximumTotalBytes: Int
    private let fileManager: FileManager

    public init(
        namespace: String,
        maximumFileBytes: Int,
        maximumTotalBytes: Int,
        directory: URL? = nil,
        fileManager: FileManager = .default
    ) throws {
        guard CaptureConfiguration.validOpaqueReference(namespace, maximum: 128),
              maximumFileBytes > 0, maximumTotalBytes >= maximumFileBytes else {
            throw IdenqaError.invalidConfiguration
        }
        let root = directory ?? fileManager.urls(for: .cachesDirectory, in: .userDomainMask)[0]
            .appending(path: "dev.idenqa.capture", directoryHint: .isDirectory)
        self.directory = root.appending(path: namespace, directoryHint: .isDirectory)
        self.maximumFileBytes = maximumFileBytes
        self.maximumTotalBytes = maximumTotalBytes
        self.fileManager = fileManager
    }

    public func stage(_ artifact: CapturedArtifact, category: String, now: Date) async throws -> CaptureTemporaryFile {
        guard CaptureConfiguration.validOpaqueReference(category, maximum: 64) else {
            throw IdenqaError.invalidConfiguration
        }
        guard artifact.contentType == "image/jpeg" || artifact.contentType == "image/png" else {
            throw IdenqaError.invalidConfiguration
        }
        guard !artifact.bytes.isEmpty, artifact.bytes.count <= maximumFileBytes else {
            throw IdenqaError.temporaryStorage
        }
        try ensureDirectory()
        let existing = try await totalBytes()
        guard existing + artifact.bytes.count <= maximumTotalBytes else { throw IdenqaError.temporaryStorage }
        let reference = "\(category)-\(UUID().uuidString).bin"
        let url = directory.appending(path: reference)
        var options: Data.WritingOptions = [.atomic]
        #if os(iOS)
        options.insert(.completeFileProtection)
        #endif
        try artifact.bytes.write(to: url, options: options)
        #if os(iOS) || os(macOS)
        try? fileManager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
        #endif
        return CaptureTemporaryFile(reference: reference, byteCount: artifact.bytes.count, createdAt: now)
    }

    public func data(for file: CaptureTemporaryFile) async throws -> Data {
        try Data(contentsOf: url(for: file))
    }

    public func remove(_ file: CaptureTemporaryFile) async throws {
        let url = try url(for: file)
        guard fileManager.fileExists(atPath: url.path) else { return }
        try fileManager.removeItem(at: url)
    }

    public func purge(olderThan date: Date) async throws -> Int {
        var removed = 0
        for entry in try contents() where entry.modified < date {
            try? fileManager.removeItem(at: entry.url)
            removed += 1
        }
        return removed
    }

    public func purgeAll() async throws -> Int {
        var removed = 0
        for entry in try contents() {
            try? fileManager.removeItem(at: entry.url)
            removed += 1
        }
        return removed
    }

    public func count() async throws -> Int {
        try contents().count
    }

    public func totalBytes() async throws -> Int {
        try contents().reduce(0) { $0 + $1.size }
    }

    private func ensureDirectory() throws {
        var isDirectory: ObjCBool = false
        if fileManager.fileExists(atPath: directory.path, isDirectory: &isDirectory) {
            guard isDirectory.boolValue else { throw IdenqaError.temporaryStorage }
            return
        }
        try fileManager.createDirectory(
            at: directory,
            withIntermediateDirectories: true,
            attributes: [.posixPermissions: 0o700]
        )
    }

    private func url(for file: CaptureTemporaryFile) throws -> URL {
        guard !file.reference.isEmpty,
              file.reference == URL(fileURLWithPath: file.reference).lastPathComponent,
              !file.reference.contains("/") else {
            throw IdenqaError.invalidConfiguration
        }
        return directory.appending(path: file.reference)
    }

    private func contents() throws -> [Entry] {
        guard fileManager.fileExists(atPath: directory.path) else { return [] }
        let urls = try fileManager.contentsOfDirectory(
            at: directory,
            includingPropertiesForKeys: [.fileSizeKey, .contentModificationDateKey],
            options: [.skipsHiddenFiles]
        )
        return try urls.map { url in
            let values = try url.resourceValues(forKeys: [.fileSizeKey, .contentModificationDateKey])
            return Entry(url: url, size: values.fileSize ?? 0, modified: values.contentModificationDate ?? .distantPast)
        }
    }

    private struct Entry {
        let url: URL
        let size: Int
        let modified: Date
    }
}
