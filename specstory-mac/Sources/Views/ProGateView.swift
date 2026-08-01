import SwiftUI
import SpecStoryKit

/// The Granola gating pattern: the real page renders underneath (with sample
/// data when locked), blurred and non-interactive, while a centered card
/// carries the plan, price, and upgrade action.
struct ProGateView<Content: View>: View {
    let gate: FeatureGate
    let featureBlurb: String        // "Analytics for your coding sessions are part of SpecStory Pro."
    let planName: String
    let planPrice: String           // "$25/mo"
    let onUpgrade: () -> Void
    let onManagePlan: (() -> Void)?
    @ViewBuilder let content: () -> Content

    var body: some View {
        switch gate {
        case .enabled:
            content()
        case .hidden, .upsell:
            ZStack {
                content()
                    .blur(radius: 5)
                    .saturation(0.9)
                    .allowsHitTesting(false)
                    .accessibilityHidden(true)

                // Soft wash so the blur reads as locked, not broken.
                Theme.paper.opacity(0.12)
                    .allowsHitTesting(false)

                upgradeCard
            }
            .clipped()
        }
    }

    private var upgradeCard: some View {
        VStack(spacing: 14) {
            Text(featureBlurb)
                .font(Theme.body(13))
                .foregroundStyle(Theme.inkSecondary)
                .multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)

            VStack(spacing: 2) {
                Text(planName)
                    .font(Theme.display(26))
                    .foregroundStyle(Theme.ink)
                Text(planPrice)
                    .font(Theme.body(13))
                    .foregroundStyle(Theme.inkSecondary)
            }

            Button(action: onUpgrade) {
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
            .buttonStyle(.plain)

            if let onManagePlan {
                Button("Or manage your existing plan") {
                    onManagePlan()
                }
                .buttonStyle(.link)
                .font(Theme.body(11))
            }
        }
        .padding(28)
        .frame(maxWidth: 380)
        .background(Theme.card, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 16, style: .continuous).strokeBorder(Theme.hairline))
        .shadow(color: .black.opacity(0.18), radius: 30, y: 10)
    }
}
