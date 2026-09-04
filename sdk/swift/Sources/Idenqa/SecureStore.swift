import Foundation
#if canImport(Security)
import Security
#endif

#if canImport(Security)
public actor KeychainCaptureTokenStore: CaptureTokenStore {
    private let service: String
    private let account: String
    private let accessGroup: String?

    public init(service: String = "dev.idenqa.capture", account: String = "capture-token", accessGroup: String? = nil) throws {
        guard !service.isEmpty, !account.isEmpty else { throw IdenqaError.invalidConfiguration }
        self.service = service
        self.account = account
        self.accessGroup = accessGroup
    }

    public func read() throws -> String? {
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

    public func write(_ token: String) throws {
        guard !token.isEmpty else { throw IdenqaError.invalidConfiguration }
        let query = baseQuery()
        SecItemDelete(query as CFDictionary)
        var value = query
        value[kSecValueData as String] = Data(token.utf8)
        value[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        guard SecItemAdd(value as CFDictionary, nil) == errSecSuccess else { throw IdenqaError.secureStorage }
    }

    public func clear() throws {
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
#endif
