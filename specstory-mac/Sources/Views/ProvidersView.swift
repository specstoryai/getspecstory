import SwiftUI
import SpecStoryKit

/// Provider management: install health from `specstory check`, notification
/// preferences, store locations.
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

                ForEach(model.providerStatuses) { status in
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
}

struct ProviderRow: View {
    @ObservedObject var model: AppModel
    let status: ProviderStatus

    var body: some View {
        HStack(spacing: 12) {
            ZStack {
                RoundedRectangle(cornerRadius: 7, style: .continuous)
                    .fill(status.provider.badgeColor.opacity(0.14))
                    .frame(width: 30, height: 30)
                Text(String(status.provider.displayName.prefix(1)))
                    .font(Theme.body(13, weight: .semibold))
                    .foregroundStyle(status.provider.badgeColor)
            }

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
