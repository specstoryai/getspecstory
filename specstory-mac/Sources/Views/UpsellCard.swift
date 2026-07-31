import SwiftUI

/// Copy block for an upsell surface. Instances live on ProModel
/// (skillsUpsell, resumeUpsell, analyticsUpsell).
struct UpsellCopy {
    let icon: String
    let title: String
    let blurb: String
    let bullets: [String]
}

/// Reusable upgrade card: icon, title, one-sentence blurb, feature bullets,
/// and the Upgrade action. Used by the Skills panel and available for
/// analytics and resume surfaces.
struct UpsellCard: View {
    let copy: UpsellCopy
    var upgradeTitle = "Upgrade to Pro"
    var busy = false
    let upgradeAction: () -> Void
    /// Shown as a quiet "Manage plan" link when present (signed-in users).
    var manageAction: (() -> Void)?
    /// Optional extra line under the actions, e.g. a sign-in hint.
    var footnote: String?

    var body: some View {
        VStack(spacing: 16) {
            ZStack {
                Circle()
                    .fill(Theme.accent.opacity(0.12))
                    .frame(width: 56, height: 56)
                Image(systemName: copy.icon)
                    .font(.system(size: 24, weight: .light))
                    .foregroundStyle(Theme.accent)
            }

            VStack(spacing: 6) {
                Text(copy.title)
                    .font(Theme.display(20))
                    .foregroundStyle(Theme.ink)
                    .multilineTextAlignment(.center)
                Text(copy.blurb)
                    .font(Theme.body(12.5))
                    .foregroundStyle(Theme.inkSecondary)
                    .multilineTextAlignment(.center)
                    .lineSpacing(2)
                    .frame(maxWidth: 360)
            }

            VStack(alignment: .leading, spacing: 8) {
                ForEach(copy.bullets, id: \.self) { bullet in
                    HStack(alignment: .firstTextBaseline, spacing: 8) {
                        Image(systemName: "checkmark.circle.fill")
                            .font(.system(size: 12))
                            .foregroundStyle(Theme.accent)
                        Text(bullet)
                            .font(Theme.body(12))
                            .foregroundStyle(Theme.ink)
                    }
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 8)

            VStack(spacing: 10) {
                Button(action: upgradeAction) {
                    Group {
                        if busy {
                            ProgressView().controlSize(.small)
                        } else {
                            Text(upgradeTitle)
                                .font(Theme.body(13, weight: .semibold))
                        }
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 4)
                }
                .buttonStyle(.borderedProminent)
                .tint(Theme.accent)
                .disabled(busy)

                if let manageAction {
                    Button("Manage plan", action: manageAction)
                        .buttonStyle(.plain)
                        .font(Theme.body(12))
                        .foregroundStyle(Theme.accent)
                        .disabled(busy)
                }

                if let footnote {
                    Text(footnote)
                        .font(Theme.body(11))
                        .foregroundStyle(Theme.inkTertiary)
                        .multilineTextAlignment(.center)
                }
            }
        }
        .padding(28)
        .frame(maxWidth: 420)
        .cardChrome()
    }
}
