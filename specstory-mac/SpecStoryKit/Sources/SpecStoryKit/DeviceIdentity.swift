import CryptoKit
import Foundation

/// Reproduces the CLI's device id derivation (pkg/cloud/sync.go getDeviceID)
/// so cloud sessions synced from this machine are recognized as local:
/// sha256 of the first non-loopback interface's raw MAC bytes, falling back
/// to sha256("specstory-" + hostname).
public enum DeviceIdentity {
    public static let current: String = computeDeviceID()

    public static var machineName: String {
        Host.current().localizedName ?? ProcessInfo.processInfo.hostName
    }

    static func computeDeviceID() -> String {
        if let mac = firstNonLoopbackMAC() {
            return SHA256.hash(data: mac).map { String(format: "%02x", $0) }.joined()
        }
        let hostname = ProcessInfo.processInfo.hostName
        return SHA256.hash(data: Data(("specstory-" + hostname).utf8)).map { String(format: "%02x", $0) }.joined()
    }

    /// First non-loopback interface with a hardware address, in interface
    /// index order to match Go's net.Interfaces() ordering.
    static func firstNonLoopbackMAC() -> Data? {
        var addrList: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&addrList) == 0, let first = addrList else { return nil }
        defer { freeifaddrs(addrList) }

        var byIndex = [(index: UInt32, mac: Data)]()
        var cursor: UnsafeMutablePointer<ifaddrs>? = first
        while let entry = cursor {
            defer { cursor = entry.pointee.ifa_next }
            let flags = Int32(entry.pointee.ifa_flags)
            guard flags & IFF_LOOPBACK == 0,
                  let addr = entry.pointee.ifa_addr,
                  addr.pointee.sa_family == UInt8(AF_LINK) else { continue }
            let mac = addr.withMemoryRebound(to: sockaddr_dl.self, capacity: 1) { dl -> Data? in
                let length = Int(dl.pointee.sdl_alen)
                guard length > 0 else { return nil }
                let nameLength = Int(dl.pointee.sdl_nlen)
                return withUnsafeBytes(of: dl.pointee.sdl_data) { raw in
                    guard nameLength + length <= raw.count else { return nil }
                    return Data(raw[nameLength..<(nameLength + length)])
                }
            }
            if let mac, mac.contains(where: { $0 != 0 }) {
                let index = if_nametoindex(String(cString: entry.pointee.ifa_name))
                byIndex.append((index, mac))
            }
        }
        return byIndex.sorted { $0.index < $1.index }.first?.mac
    }
}
