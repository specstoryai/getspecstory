import Foundation
import Security

public enum KeychainError: Error {
    case unexpectedStatus(OSStatus)
}

/// Generic-password storage scoped to one service (the app uses
/// "com.specstory.mac"). Untested by design: headless keychain access is
/// flaky in CI, so this stays tiny and obvious.
public final class KeychainStore {
    private let service: String
    // Test hook: an in-memory backing so AuthManager tests never touch the
    // real keychain.
    private var memory: [String: String]?
    private let lock = NSLock()

    public init(service: String) {
        self.service = service
        self.memory = nil
    }

    static func ephemeral(service: String = "com.specstory.mac.tests") -> KeychainStore {
        let store = KeychainStore(service: service)
        store.memory = [:]
        return store
    }

    public func string(for key: String) -> String? {
        lock.lock()
        defer { lock.unlock() }
        if memory != nil { return memory?[key] }

        var query = baseQuery(key: key)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: AnyObject?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        guard status == errSecSuccess, let data = result as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    public func set(_ value: String, for key: String) throws {
        lock.lock()
        defer { lock.unlock() }
        if memory != nil {
            memory?[key] = value
            return
        }

        let data = Data(value.utf8)
        let query = baseQuery(key: key)
        let update: [String: Any] = [kSecValueData as String: data]
        let updateStatus = SecItemUpdate(query as CFDictionary, update as CFDictionary)
        if updateStatus == errSecSuccess { return }
        guard updateStatus == errSecItemNotFound else {
            throw KeychainError.unexpectedStatus(updateStatus)
        }
        var add = query
        add[kSecValueData as String] = data
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
        let addStatus = SecItemAdd(add as CFDictionary, nil)
        guard addStatus == errSecSuccess else {
            throw KeychainError.unexpectedStatus(addStatus)
        }
    }

    public func delete(_ key: String) {
        lock.lock()
        defer { lock.unlock() }
        if memory != nil {
            memory?[key] = nil
            return
        }
        SecItemDelete(baseQuery(key: key) as CFDictionary)
    }

    private func baseQuery(key: String) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key,
        ]
    }
}
