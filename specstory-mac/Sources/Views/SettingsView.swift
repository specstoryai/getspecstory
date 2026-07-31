import SwiftUI
import SpecStoryKit

/// Account (device-flow sign in), sync, notifications, startup, about.
struct SettingsView: View {
    @ObservedObject var model: AppModel
    @State private var deviceCode = ""
    @State private var signingIn = false
    @State private var signInError: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                Text("Settings")
                    .font(Theme.display(24))
                    .foregroundStyle(Theme.ink)

                accountCard
                generalCard
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
                HStack(spacing: 8) {
                    Button("Get code in browser") {
                        model.beginSignIn()
                    }
                    .controlSize(.small)
                    TextField("ABC-123", text: $deviceCode)
                        .textFieldStyle(.roundedBorder)
                        .font(Theme.mono(13))
                        .frame(width: 110)
                        .onSubmit { submitCode() }
                    Button(signingIn ? "Connecting" : "Connect") {
                        submitCode()
                    }
                    .controlSize(.small)
                    .buttonStyle(.borderedProminent)
                    .tint(Theme.accent)
                    .disabled(signingIn || deviceCode.trimmingCharacters(in: .whitespaces).isEmpty)
                }
                if let signInError {
                    Text(signInError)
                        .font(Theme.body(11))
                        .foregroundStyle(Theme.live)
                }
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardChrome()
    }

    private func submitCode() {
        let code = deviceCode.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !code.isEmpty else { return }
        signingIn = true
        signInError = nil
        Task {
            do {
                try await model.completeSignIn(code: code)
                deviceCode = ""
            } catch {
                signInError = error.localizedDescription
            }
            signingIn = false
        }
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
