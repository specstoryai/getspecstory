import SwiftUI
import SpecStoryKit

/// One session row in the feed: provider mark, title, context line, origin
/// and machine badges, hover-revealed actions. The Granola card unit.
struct SessionCardView: View {
    let item: SessionItem
    var isLive = false
    var currentDeviceID: String? = nil
    var onOpen: () -> Void = {}
    var onResume: (() -> Void)? = nil

    @State private var hovering = false

    var body: some View {
        HStack(alignment: .center, spacing: 12) {
            providerMark

            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(item.title)
                        .font(Theme.body(13, weight: .medium))
                        .foregroundStyle(Theme.ink)
                        .lineLimit(1)
                    if isLive {
                        LivePill()
                    }
                }
                contextLine
            }

            Spacer(minLength: 12)

            trailing
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
        .contentShape(RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous))
        .cardChrome(hovering: hovering)
        .onHover { hovering = $0 }
        .onTapGesture(perform: onOpen)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("\(item.title), \(providerDisplayName)")
        .accessibilityAddTraits(.isButton)
    }

    private var providerMark: some View {
        ZStack {
            RoundedRectangle(cornerRadius: 7, style: .continuous)
                .fill(providerColor.opacity(0.14))
                .frame(width: 30, height: 30)
            Text(String(providerDisplayName.prefix(1)))
                .font(Theme.body(13, weight: .semibold))
                .foregroundStyle(providerColor)
        }
    }

    private var contextLine: some View {
        HStack(spacing: 5) {
            Text(providerDisplayName)
                .foregroundStyle(providerColor)
            if let project = item.projectName {
                Text("·").foregroundStyle(Theme.inkTertiary)
                Text(project)
            }
            if item.isFromOtherMachine(currentDeviceID: currentDeviceID), let machine = item.machineName {
                Text("·").foregroundStyle(Theme.inkTertiary)
                Label(machine, systemImage: "laptopcomputer")
                    .labelStyle(.titleAndIcon)
            }
            if let prompts = item.promptCount, prompts > 0 {
                Text("·").foregroundStyle(Theme.inkTertiary)
                Text("\(prompts) prompt\(prompts == 1 ? "" : "s")")
            }
        }
        .font(Theme.body(11))
        .foregroundStyle(Theme.inkSecondary)
        .lineLimit(1)
    }

    private var trailing: some View {
        HStack(spacing: 10) {
            if hovering, let onResume {
                Button(action: onResume) {
                    Label("Resume", systemImage: "arrow.uturn.forward")
                        .font(Theme.body(11, weight: .medium))
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .transition(.opacity)
            }
            VStack(alignment: .trailing, spacing: 3) {
                Text(relativeTime)
                    .font(Theme.body(11))
                    .foregroundStyle(Theme.inkTertiary)
                    .monospacedDigit()
                originBadge
            }
        }
        .animation(.easeOut(duration: 0.12), value: hovering)
    }

    @ViewBuilder private var originBadge: some View {
        switch item.origin {
        case .both:
            Image(systemName: "checkmark.icloud")
                .font(.system(size: 10))
                .foregroundStyle(Theme.synced)
                .help("Synced to SpecStory Cloud")
        case .cloudOnly:
            Image(systemName: "icloud")
                .font(.system(size: 10))
                .foregroundStyle(Theme.inkTertiary)
                .help("In SpecStory Cloud")
        case .localOnly:
            Image(systemName: "internaldrive")
                .font(.system(size: 10))
                .foregroundStyle(Theme.inkTertiary)
                .help("On this Mac only")
        }
    }

    private var providerDisplayName: String {
        Provider(providerID: item.provider)?.displayName ?? item.provider.capitalized
    }

    private var providerColor: Color {
        Provider(providerID: item.provider)?.badgeColor ?? Theme.inkSecondary
    }

    private var relativeTime: String {
        guard let date = item.updatedAt ?? item.createdAt else { return "" }
        if isLive || Date().timeIntervalSince(date) < 90 {
            return "now"
        }
        let formatter = RelativeDateTimeFormatter()
        formatter.unitsStyle = .abbreviated
        return formatter.localizedString(for: date, relativeTo: Date())
    }
}

/// Small red "Live" indicator with a breathing dot.
struct LivePill: View {
    @State private var pulsing = false

    var body: some View {
        HStack(spacing: 4) {
            Circle()
                .fill(Theme.live)
                .frame(width: 6, height: 6)
                .opacity(pulsing ? 0.35 : 1)
                .animation(.easeInOut(duration: 0.9).repeatForever(autoreverses: true), value: pulsing)
            Text("Live")
                .font(Theme.body(10, weight: .semibold))
                .foregroundStyle(Theme.live)
        }
        .padding(.horizontal, 7)
        .padding(.vertical, 2)
        .background(Capsule().fill(Theme.live.opacity(0.12)))
        .onAppear { pulsing = true }
        .accessibilityLabel("Recording now")
    }
}
