import SwiftUI
import SpecStoryKit

/// Shown when resuming a cloud session without the Pro entitlement: what the
/// feature does, the plan, and the upgrade action (Granola pattern, sheet
/// form).
struct ResumeUpsellSheet: View {
    @ObservedObject var model: AppModel

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "arrow.uturn.forward.circle")
                .font(.system(size: 30, weight: .light))
                .foregroundStyle(Theme.accent)

            Text("Resume sessions from any machine")
                .font(Theme.display(22))
                .foregroundStyle(Theme.ink)

            Text("Cloud resume rebuilds a synced session inside your agent, even when it was recorded on another machine. It is part of SpecStory Pro.")
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
                .multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)

            VStack(spacing: 2) {
                Text("Pro")
                    .font(Theme.display(24))
                    .foregroundStyle(Theme.ink)
                Text("$25/mo")
                    .font(Theme.body(13))
                    .foregroundStyle(Theme.inkSecondary)
            }

            Button {
                model.pro.openCheckout()
                model.resumeUpsellShown = false
            } label: {
                HStack(spacing: 6) {
                    Text("Upgrade")
                        .font(Theme.body(13, weight: .semibold))
                    Image(systemName: "arrow.up.circle")
                        .font(.system(size: 13, weight: .semibold))
                }
                .foregroundStyle(Color.white)
                .padding(.horizontal, 22)
                .padding(.vertical, 9)
                .background(Theme.ink, in: Capsule())
            }
            .buttonStyle(.tactile)

            Button("Not now") {
                model.resumeUpsellShown = false
            }
            .buttonStyle(.link)
            .font(Theme.body(11))
            .keyboardShortcut(.escape, modifiers: [])
        }
        .padding(28)
        .frame(width: 400)
    }
}
