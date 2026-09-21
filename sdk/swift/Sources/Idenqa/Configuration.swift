import Foundation

/// Bounded, non-secret capture SDK configuration.
///
/// The configuration deliberately has no token, API-key, or credential field.
/// Capture credentials are accepted only through the bootstrap credential flow
/// (`CaptureJourney.start(bootstrapToken:)`) and are stored through the
/// platform secure store.
public struct CaptureConfiguration: Equatable, Sendable {
    public let coreURL: URL
    public let applicationID: String
    public let installationID: String
    public let locale: String
    public let region: String?
    public let captureProfileID: String?
    public let preferredMethods: [String]
    public let maximumUploadBytes: Int
    public let maximumTemporaryBytes: Int

    public init(
        coreURL: URL,
        applicationID: String,
        installationID: String,
        locale: String,
        region: String? = nil,
        captureProfileID: String? = nil,
        preferredMethods: [String] = [],
        maximumUploadBytes: Int = 16 * 1024 * 1024,
        maximumTemporaryBytes: Int = 64 * 1024 * 1024
    ) throws {
        guard coreURL.scheme == "https", coreURL.user == nil, coreURL.password == nil,
              coreURL.query == nil, coreURL.fragment == nil, coreURL.host != nil else {
            throw IdenqaError.invalidConfiguration
        }
        guard Self.validApplicationID(applicationID) else { throw IdenqaError.invalidConfiguration }
        guard Self.validOpaqueReference(installationID, maximum: 128) else { throw IdenqaError.invalidConfiguration }
        guard Self.validLocale(locale) else { throw IdenqaError.invalidConfiguration }
        if let region {
            guard Self.validRegion(region) else { throw IdenqaError.invalidConfiguration }
        }
        if let captureProfileID {
            guard Self.validOpaqueReference(captureProfileID, maximum: 160), captureProfileID.hasPrefix("prf_") else {
                throw IdenqaError.invalidConfiguration
            }
        }
        guard preferredMethods.count <= 64, Set(preferredMethods).count == preferredMethods.count,
              preferredMethods.allSatisfy(Self.validMethod) else {
            throw IdenqaError.invalidConfiguration
        }
        guard (1_024...67_108_864).contains(maximumUploadBytes) else { throw IdenqaError.invalidConfiguration }
        guard maximumTemporaryBytes >= maximumUploadBytes, maximumTemporaryBytes <= 268_435_456 else {
            throw IdenqaError.invalidConfiguration
        }
        self.coreURL = coreURL
        self.applicationID = applicationID
        self.installationID = installationID
        self.locale = locale
        self.region = region
        self.captureProfileID = captureProfileID
        self.preferredMethods = preferredMethods
        self.maximumUploadBytes = maximumUploadBytes
        self.maximumTemporaryBytes = maximumTemporaryBytes
    }

    /// Bounded, non-empty ASCII reference using the native bootstrap alphabet.
    public static func validOpaqueReference(_ value: String, maximum: Int) -> Bool {
        !value.isEmpty && value.utf8.count <= maximum && value.allSatisfy {
            $0.isASCII && ($0.isLetter || $0.isNumber || "._-".contains($0))
        }
    }

    /// Namespaced acquisition-method name. Unknown namespaced methods fail closed during selection.
    public static func validMethod(_ value: String) -> Bool {
        guard validOpaqueReference(value, maximum: 128), value.contains(".") else { return false }
        return !value.hasPrefix(".") && !value.hasSuffix(".")
    }

    public static func validApplicationID(_ value: String) -> Bool {
        validOpaqueReference(value, maximum: 255)
    }

    public static func validRegion(_ value: String) -> Bool {
        guard let first = value.first, first.isLowercaseASCII, value.utf8.count <= 63 else { return false }
        return value.allSatisfy { $0.isLowercaseASCII || $0.isNumber || $0 == "-" }
    }

    public static func validLocale(_ value: String) -> Bool {
        let parts = value.split(separator: "-", omittingEmptySubsequences: false)
        guard let language = parts.first, (2...8).contains(language.count),
              language.allSatisfy(\.isLetterASCII), value.utf8.count <= 64 else { return false }
        return parts.dropFirst().allSatisfy { part in
            (1...8).contains(part.count) && part.allSatisfy { $0.isLetterASCII || $0.isNumber }
        }
    }
}

private extension Character {
    var isLetterASCII: Bool { isASCII && isLetter }
    var isLowercaseASCII: Bool { isASCII && isLowercase }
}
