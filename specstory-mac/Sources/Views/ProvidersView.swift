import SwiftUI
import SpecStoryKit

/// Provider management, grouped: IDE integrations first, then terminal
/// agents. Health from `specstory check`; muting silences notifications only.
struct ProvidersView: View {
    @ObservedObject var model: AppModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 8) {
                Text("Providers")
                    .font(Theme.display(24))
                    .foregroundStyle(Theme.ink)
                Text("SpecStory watches these agents automatically. Muting a provider only silences its notifications; sessions are always saved.")
                    .font(Theme.body(12))
                    .foregroundStyle(Theme.inkSecondary)
                    .padding(.bottom, 10)

                sectionHeader("In your IDE")
                ForEach(ideStatuses) { status in
                    ProviderRow(model: model, status: status)
                }

                sectionHeader("In your terminal")
                    .padding(.top, 14)
                ForEach(terminalStatuses) { status in
                    ProviderRow(model: model, status: status)
                }
            }
            .padding(28)
            .frame(maxWidth: Theme.feedWidth, alignment: .leading)
            .frame(maxWidth: .infinity)
        }
        .background(Theme.paper)
        .task {
            await model.refreshProviderStatuses()
        }
    }

    private var ideStatuses: [ProviderStatus] {
        model.providerStatuses.filter { $0.provider.isIDE }
    }

    private var terminalStatuses: [ProviderStatus] {
        model.providerStatuses.filter { !$0.provider.isIDE }
    }

    private func sectionHeader(_ title: String) -> some View {
        Text(title)
            .font(Theme.body(11, weight: .semibold))
            .foregroundStyle(Theme.inkTertiary)
            .textCase(.uppercase)
            .kerning(0.4)
            .padding(.bottom, 2)
    }
}

struct ProviderRow: View {
    @ObservedObject var model: AppModel
    let status: ProviderStatus

    var body: some View {
        HStack(spacing: 12) {
            ProviderIcon(provider: status.provider, size: 32)

            VStack(alignment: .leading, spacing: 2) {
                Text(status.provider.displayName)
                    .font(Theme.body(13, weight: .medium))
                    .foregroundStyle(Theme.ink)
                healthLabel
            }

            Spacer()

            Toggle("Notifications", isOn: Binding(
                get: { status.notificationsOn },
                set: { model.setProviderNotifications(status.provider, on: $0) }
            ))
            .toggleStyle(.switch)
            .controlSize(.small)
            .labelsHidden()
            .help("Notify when a new \(status.provider.displayName) session starts")
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
        .cardChrome()
    }

    @ViewBuilder private var healthLabel: some View {
        if !status.provider.watchableByCLI {
            Label("Captured by the SpecStory extension in VS Code", systemImage: "puzzlepiece.extension")
                .font(Theme.body(11))
                .foregroundStyle(Theme.inkSecondary)
                .labelStyle(.titleAndIcon)
        } else {
            switch status.healthy {
            case .none:
                HStack(spacing: 5) {
                    ProgressView().controlSize(.mini)
                    Text("Checking")
                }
                .font(Theme.body(11))
                .foregroundStyle(Theme.inkTertiary)
            case .some(true):
                Label("Ready", systemImage: "checkmark.circle.fill")
                    .font(Theme.body(11))
                    .foregroundStyle(Theme.synced)
                    .labelStyle(.titleAndIcon)
            case .some(false):
                Label("Not detected on this Mac", systemImage: "minus.circle")
                    .font(Theme.body(11))
                    .foregroundStyle(Theme.inkTertiary)
                    .labelStyle(.titleAndIcon)
            }
        }
    }
}
