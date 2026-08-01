import SwiftUI
import SpecStoryKit

/// Account (device-flow sign in), sync, notifications, startup, about.
struct SettingsView: View {
    @ObservedObject var model: AppModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                Text("Settings")
                    .font(Theme.display(24))
                    .foregroundStyle(Theme.ink)

                accountCard
                generalCard
                cliCard
                aboutCard
            }
            .padding(28)
            .frame(maxWidth: 560, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .center)
        }
        .background(Theme.paper)
    }

    // MARK: Account

    private var accountCard: some View {
        VStack(alignment: .leading, spacing: 12) {
            sectionTitle("SpecStory Cloud")
            if let email = model.signedInEmail {
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(email)
                            .font(Theme.body(13, weight: .medium))
                            .foregroundStyle(Theme.ink)
                        Text("Sessions sync to your account and power Ask and cross-machine browsing.")
                            .font(Theme.body(11))
                            .foregroundStyle(Theme.inkSecondary)
                    }
                    Spacer()
                    Button("Sign out") {
                        Task { await model.signOut() }
                    }
                    .controlSize(.small)
                }
            } else {
                Text("Sign in to sync sessions, browse other machines, and ask questions across your whole history.")
                    .font(Theme.body(12))
                    .foregroundStyle(Theme.inkSecondary)
                Button("Sign in to SpecStory Cloud") {
                    model.signInSheetShown = true
                }
                .buttonStyle(.borderedProminent)
                .tint(Theme.accent)
                .controlSize(.regular)
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardChrome()
    }

    // MARK: General

    private var generalCard: some View {
        VStack(alignment: .leading, spacing: 12) {
            sectionTitle("General")
            Toggle(isOn: $model.cloudSyncEnabled) {
                settingLabel("Sync sessions to Cloud", detail: "Watched sessions upload to your SpecStory Cloud account as you work.")
            }
            .toggleStyle(.switch)
            Toggle(isOn: $model.notificationsEnabled) {
                settingLabel("Session notifications", detail: "Get notified when a new session starts in any agent. Per-provider muting lives in Providers.")
            }
            .toggleStyle(.switch)
            Toggle(isOn: $model.launchAtLoginEnabled) {
                settingLabel("Launch at login", detail: "Keep SpecStory in your menu bar so no session goes unrecorded.")
            }
            .toggleStyle(.switch)
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardChrome()
    }

    private func settingLabel(_ title: String, detail: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title)
                .font(Theme.body(13, weight: .medium))
                .foregroundStyle(Theme.ink)
            Text(detail)
                .font(Theme.body(11))
                .foregroundStyle(Theme.inkSecondary)
        }
        .padding(.trailing, 8)
    }

    // MARK: CLI

    private var cliCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            sectionTitle("Command line tool")
            Text("The app bundles its own engine, so nothing here is required. Installing the CLI in your PATH makes terminal resume commands portable and keeps them working if the app moves.")
                .font(Theme.body(11))
                .foregroundStyle(Theme.inkSecondary)
                .fixedSize(horizontal: false, vertical: true)

            switch model.updates.cliStatus {
            case .checking, .unknown:
                HStack(spacing: 6) {
                    ProgressView().controlSize(.mini)
                    Text("Checking your installed CLI")
                        .font(Theme.body(12))
                        .foregroundStyle(Theme.inkSecondary)
                }
            case .upToDate(let version):
                Label("Installed and current (\(version))", systemImage: "checkmark.circle.fill")
                    .font(Theme.body(12))
                    .foregroundStyle(Theme.synced)
            case .updateAvailable(let installed, let latest):
                VStack(alignment: .leading, spacing: 8) {
                    Label("\(installed) installed, \(latest) available", systemImage: "arrow.up.circle")
                        .font(Theme.body(12))
                        .foregroundStyle(Theme.ink)
                    updateButton(title: "Update CLI in Terminal")
                }
            case .notInstalled(let latest):
                VStack(alignment: .leading, spacing: 8) {
                    Label(latest.map { "Not installed (latest is \($0))" } ?? "Not installed",
                          systemImage: "minus.circle")
                        .font(Theme.body(12))
                        .foregroundStyle(Theme.inkSecondary)
                    updateButton(title: "Install CLI in Terminal")
                }
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardChrome()
        .task {
            await model.updates.refreshCLIStatus()
        }
    }

    private func updateButton(title: String) -> some View {
        Button(title) {
            _ = TerminalLauncher.launch(command: UpdateChecker.installCommand, workingDirectory: nil)
        }
        .controlSize(.small)
        .help("Runs the documented installer from docs.specstory.com in your terminal")
    }

    // MARK: About

    private var aboutCard: some View {
        VStack(alignment: .leading, spacing: 8) {
            sectionTitle("About")
            let appVersion = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "dev"
            infoRow("App", appVersion)
            infoRow("Bundled CLI", bundledCLIVersion)
            if let path = AppModel.userInstalledCLI() {
                infoRow("CLI on PATH", path)
            }
            HStack(spacing: 10) {
                Button(model.updates.checkingAppUpdate ? "Checking..." : "Check for app updates") {
                    let version = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "0.0.0"
                    Task { await model.updates.checkForAppUpdate(currentVersion: version) }
                }
                .controlSize(.small)
                .disabled(model.updates.checkingAppUpdate)
                if let message = model.updates.appUpdateMessage {
                    Text(message)
                        .font(Theme.body(11))
                        .foregroundStyle(Theme.inkSecondary)
                }
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardChrome()
    }

    private var bundledCLIVersion: String {
        guard let url = Bundle.main.url(forResource: "bin/manifest", withExtension: "json"),
              let data = try? Data(contentsOf: url),
              let manifest = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let version = manifest["cliVersion"] as? String else {
            return "unknown"
        }
        return version
    }

    private func infoRow(_ label: String, _ value: String) -> some View {
        HStack {
            Text(label)
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
                .frame(width: 90, alignment: .leading)
            Text(value)
                .font(Theme.mono(11))
                .foregroundStyle(Theme.ink)
                .textSelection(.enabled)
                .lineLimit(1)
                .truncationMode(.middle)
        }
    }

    private func sectionTitle(_ title: String) -> some View {
        Text(title)
            .font(Theme.body(11, weight: .semibold))
            .foregroundStyle(Theme.inkTertiary)
            .textCase(.uppercase)
            .kerning(0.4)
    }
}
