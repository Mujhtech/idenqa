import Foundation
#if canImport(Security)
import Security
#endif

#if canImport(Security)
/// Shared Keychain string item used by the capture token and journey stores.
struct KeychainStringItem {
    let service: String
    let account: String
    let accessGroup: String?

    init(service: String, account: String, accessGroup: String?) throws {
        guard !service.isEmpty, !account.isEmpty else { throw IdenqaError.invalidConfiguration }
        self.service = service
        self.account = account
        self.accessGroup = accessGroup
    }

    func read() throws -> String? {
        var query = baseQuery()
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = result as? Data, let value = String(data: data, encoding: .utf8) else {
            throw IdenqaError.secureStorage
        }
        return value
    }

    func write(_ value: String) throws {
        guard !value.isEmpty else { throw IdenqaError.invalidConfiguration }
        let query = baseQuery()
        SecItemDelete(query as CFDictionary)
        var item = query
        item[kSecValueData as String] = Data(value.utf8)
        item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        guard SecItemAdd(item as CFDictionary, nil) == errSecSuccess else { throw IdenqaError.secureStorage }
    }

    func clear() throws {
        let status = SecItemDelete(baseQuery() as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else { throw IdenqaError.secureStorage }
    }

    private func baseQuery() -> [String: Any] {
        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        if let accessGroup { query[kSecAttrAccessGroup as String] = accessGroup }
        return query
    }
}

public actor KeychainCaptureTokenStore: CaptureTokenStore {
    private let item: KeychainStringItem

    public init(service: String = "dev.idenqa.capture", account: String = "capture-token", accessGroup: String? = nil) throws {
        self.item = try KeychainStringItem(service: service, account: account, accessGroup: accessGroup)
    }

    public func read() throws -> String? { try item.read() }
    public func write(_ token: String) throws { try item.write(token) }
    public func clear() throws { try item.clear() }
}

/// JSON codec for the persisted minimum reference. Encoding and decoding use
/// the same ISO-8601 date strategy so restarts restore expiry exactly.
enum CaptureJourneyReferenceCodec {
    static func encode(_ reference: CaptureJourneyReference) throws -> String {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        encoder.dateEncodingStrategy = .iso8601
        let data = try encoder.encode(reference)
        guard let encoded = String(data: data, encoding: .utf8) else { throw IdenqaError.secureStorage }
        return encoded
    }

    static func decode(_ encoded: String) throws -> CaptureJourneyReference {
        guard let data = encoded.data(using: .utf8) else { throw IdenqaError.secureStorage }
        do {
            return try JSONDecoder.idenqa.decode(CaptureJourneyReference.self, from: data)
        } catch {
            throw IdenqaError.secureStorage
        }
    }
}

/// Keychain-backed minimum-reference journey store. Never stores evidence bytes,
/// notice copy, or subject data.
public actor KeychainCaptureJourneyStore: CaptureJourneyStore {
    private let item: KeychainStringItem

    public init(service: String = "dev.idenqa.capture", account: String = "capture-journey", accessGroup: String? = nil) throws {
        guard account != "capture-token" else { throw IdenqaError.invalidConfiguration }
        self.item = try KeychainStringItem(service: service, account: account, accessGroup: accessGroup)
    }

    public func read() throws -> CaptureJourneyReference? {
        guard let encoded = try item.read() else { return nil }
        return try CaptureJourneyReferenceCodec.decode(encoded)
    }

    public func write(_ reference: CaptureJourneyReference) throws {
        try item.write(CaptureJourneyReferenceCodec.encode(reference))
    }

    public func clear() throws { try item.clear() }
}
#endif
