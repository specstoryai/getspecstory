import AppKit
import Foundation
import SpecStoryKit

/// Freshness checks for the two things that ship separately from this app:
/// the user's terminal CLI (installed via docs.specstory.com's installer)
/// and the app itself (GitHub releases until Sparkle lands with signed
/// builds; see RELEASING.md).
///
/// The app never depends on a user-installed CLI: the bundled, sha-pinned
/// binary drives all watching and syncing. The installed CLI only powers
/// terminal-side resume, so the check nudges rather than blocks.
@MainActor
final class UpdateChecker: ObservableObject {
    enum CLIStatus: Equatable {
        case checking
        case notInstalled(latest: String?)
        case upToDate(version: String)
        case updateAvailable(installed: String, latest: String)
        case unknown
    }

    @Published private(set) var cliStatus: CLIStatus = .unknown
    @Published private(set) var latestCLIVersion: String?
    @Published private(set) var appUpdateMessage: String?
    @Published private(set) var checkingAppUpdate = false

    private var lastCheckAt: Date = .distantPast

    static let repo = "specstoryai/getspecstory"

    /// The documented install command (docs.specstory.com); runs in the
    /// user's terminal because /usr/local/bin can require sudo.
    static let installCommand = "curl -fsSL https://raw.githubusercontent.com/\(repo)/main/install.sh | bash"

    // MARK: - CLI

    func refreshCLIStatus(force: Bool = false) async {
        guard force || Date().timeIntervalSince(lastCheckAt) > 86_400 || cliStatus == .unknown else { return }
        cliStatus = .checking
        lastCheckAt = Date()

        let latest = await fetchLatestReleaseTag(prefix: "v")
        latestCLIVersion = latest

        guard let installedPath = AppModel.userInstalledCLI() else {
            cliStatus = .notInstalled(latest: latest)
            return
        }
        let installed = await installedVersion(at: installedPath)
        guard let installed else {
            cliStatus = .notInstalled(latest: latest)
            return
        }
        if let latest, Self.versionIsNewer(latest, than: installed) {
            cliStatus = .updateAvailable(installed: installed, latest: latest)
        } else {
            cliStatus = .upToDate(version: installed)
        }
    }

    /// Runs `specstory version` and extracts a semver; nil for dev builds
    /// that report no version.
    private func installedVersion(at path: String) async -> String? {
        let output = try? await CLIRunner.run(
            binary: URL(fileURLWithPath: path), arguments: ["version"],
            workingDirectory: NSHomeDirectory(), environment: [:], timeout: 10
        )
        guard let output else { return nil }
        return Self.extractVersion(from: output)
    }

    static func extractVersion(from text: String) -> String? {
        guard let range = text.range(of: #"v?\d+\.\d+\.\d+"#, options: .regularExpression) else { return nil }
        var version = String(text[range])
        if !version.hasPrefix("v") { version = "v" + version }
        return version
    }

    /// Numeric semver comparison; "v2.10.0" beats "v2.9.9".
    static func versionIsNewer(_ candidate: String, than current: String) -> Bool {
        func parts(_ version: String) -> [Int] {
            version.trimmingCharacters(in: CharacterSet(charactersIn: "v"))
                .split(separator: ".")
                .compactMap { Int($0.prefix(while: \.isNumber)) }
        }
        let a = parts(candidate)
        let b = parts(current)
        for index in 0..<max(a.count, b.count) {
            let lhs = index < a.count ? a[index] : 0
            let rhs = index < b.count ? b[index] : 0
            if lhs != rhs { return lhs > rhs }
        }
        return false
    }

    // MARK: - App

    /// Until signed builds and Sparkle land, app updates are announced from
    /// GitHub releases tagged mac-app-v*; absent that tag this reports the
    /// build as current.
    func checkForAppUpdate(currentVersion: String) async {
        checkingAppUpdate = true
        defer { checkingAppUpdate = false }
        let latest = await fetchLatestReleaseTag(prefix: "mac-app-v")
        guard let latest else {
            appUpdateMessage = "You are on the newest available build."
            return
        }
        let normalized = latest.replacingOccurrences(of: "mac-app-", with: "")
        if Self.versionIsNewer(normalized, than: "v" + currentVersion) {
            appUpdateMessage = "Version \(normalized) is available on GitHub."
            NSWorkspaceOpen.releases()
        } else {
            appUpdateMessage = "You are on the newest available build."
        }
    }

    // MARK: - GitHub

    /// Latest release tag matching the prefix; checks /releases/latest first
    /// (the CLI's release train), then the release list for prefixed tags.
    private func fetchLatestReleaseTag(prefix: String) async -> String? {
        struct Release: Decodable { let tag_name: String }
        var request = URLRequest(url: URL(string: "https://api.github.com/repos/\(Self.repo)/releases?per_page=15")!)
        request.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        guard let (data, response) = try? await URLSession.shared.data(for: request),
              (response as? HTTPURLResponse)?.statusCode == 200,
              let releases = try? JSONDecoder().decode([Release].self, from: data) else {
            return nil
        }
        if prefix == "v" {
            // The CLI's release train: plain vN.N.N tags only.
            return releases.first { $0.tag_name.hasPrefix("v") && !$0.tag_name.hasPrefix("mac-app-") }?.tag_name
        }
        return releases.first { $0.tag_name.hasPrefix(prefix) }?.tag_name
    }
}

private enum NSWorkspaceOpen {
    static func releases() {
        if let url = URL(string: "https://github.com/\(UpdateChecker.repo)/releases") {
            NSWorkspace.shared.open(url)
        }
    }
}
