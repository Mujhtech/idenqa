import CryptoKit
import Foundation
#if canImport(Security)
import Security
#endif

public protocol NativeProofKey: Sendable {
    func publicKey() async throws -> Data
    func sign(_ message: Data) async throws -> Data
}

public protocol NativeAttestationProvider: Sendable {
    func attestation() async throws -> String?
}

public struct NativeBootstrapIdentity: Sendable {
    public let applicationID: String
    private let key: any NativeProofKey
    private let attestationProvider: (any NativeAttestationProvider)?

    public init(applicationID: String, key: any NativeProofKey, attestationProvider: (any NativeAttestationProvider)? = nil) throws {
        guard Self.validApplicationID(applicationID) else { throw IdenqaError.invalidConfiguration }
        self.applicationID = applicationID
        self.key = key
        self.attestationProvider = attestationProvider
    }

    public static func installedApplication(key: any NativeProofKey, attestationProvider: (any NativeAttestationProvider)? = nil) throws -> Self {
        guard let identifier = Bundle.main.bundleIdentifier else { throw IdenqaError.invalidConfiguration }
        return try Self(applicationID: identifier, key: key, attestationProvider: attestationProvider)
    }

    func request(token: String, capabilities: CapabilityAdvertisement, createdAt: Date) async throws -> NativeBootstrapRequest {
        let created = Int64(createdAt.timeIntervalSince1970)
        guard created > 0 else { throw IdenqaError.invalidConfiguration }
        let tokenDigest = SHA256.hash(data: Data(token.utf8)).hex
        let tuple = capabilities.platform + "\0" + capabilities.sdkVersion + "\0" + capabilities.implementedMethods.sorted().joined(separator: "\u{1f}") + "\0" + capabilities.currentlyAvailableMethods.sorted().joined(separator: "\u{1f}")
        let bodyDigest = SHA256.hash(data: Data(tuple.utf8)).hex
        let canonical = "idq-native-bootstrap\0v1\0\(applicationID)\0POST\0/v1/capture/native/bootstrap\0\(created)\0\(tokenDigest)\0\(bodyDigest)"
        let signature = try await key.sign(Data(canonical.utf8))
        let publicKey = try await key.publicKey()
        return NativeBootstrapRequest(
            applicationID: applicationID,
            proofKey: publicKey.base64URL,
            proofCreatedAt: created,
            proofAlgorithm: "ES256",
            proofFormat: "der",
            proof: signature.base64URL,
            attestation: try await attestationProvider?.attestation(),
            capabilities: capabilities
        )
    }

    private static func validApplicationID(_ value: String) -> Bool {
        !value.isEmpty && value.utf8.count <= 255 && value.allSatisfy { $0.isASCII && ($0.isLetter || $0.isNumber || "._-".contains($0)) }
    }
}

struct NativeBootstrapRequest: Codable, Sendable {
    let applicationID: String
    let proofKey: String
    let proofCreatedAt: Int64
    let proofAlgorithm: String
    let proofFormat: String
    let proof: String
    let attestation: String?
    let capabilities: CapabilityAdvertisement

    enum CodingKeys: String, CodingKey {
        case applicationID = "application_id"
        case proofKey = "proof_key"
        case proofCreatedAt = "proof_created_at"
        case proofAlgorithm = "proof_algorithm"
        case proofFormat = "proof_format"
        case proof, attestation, capabilities
    }
}

#if canImport(Security) && canImport(CryptoKit)
public actor SecureEnclaveP256ProofKey: NativeProofKey {
    private let tag: String
    private var key: SecureEnclave.P256.Signing.PrivateKey?

    public init(tag: String = "dev.idenqa.native-proof.v1") throws {
        guard !tag.isEmpty, SecureEnclave.isAvailable else { throw IdenqaError.hardwareSecurityUnavailable }
        self.tag = tag
    }

    public func publicKey() throws -> Data { try load().publicKey.x963Representation }

    public func sign(_ message: Data) throws -> Data {
        try load().signature(for: message).derRepresentation
    }

    private func load() throws -> SecureEnclave.P256.Signing.PrivateKey {
        if let key { return key }
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: tag,
            kSecAttrAccount as String: "p256-signing-key",
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        let loaded: SecureEnclave.P256.Signing.PrivateKey
        if status == errSecSuccess, let data = result as? Data {
            loaded = try SecureEnclave.P256.Signing.PrivateKey(dataRepresentation: data)
        } else if status == errSecItemNotFound {
            loaded = try SecureEnclave.P256.Signing.PrivateKey()
            var item = query
            item.removeValue(forKey: kSecReturnData as String)
            item.removeValue(forKey: kSecMatchLimit as String)
            item[kSecValueData as String] = loaded.dataRepresentation
            item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
            guard SecItemAdd(item as CFDictionary, nil) == errSecSuccess else { throw IdenqaError.secureStorage }
        } else {
            throw IdenqaError.secureStorage
        }
        key = loaded
        return loaded
    }
}
#endif

private extension Data {
    var base64URL: String { base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "") }
}

private extension SHA256.Digest {
    var hex: String { map { String(format: "%02x", $0) }.joined() }
}
