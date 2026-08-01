import SwiftUI
import SpecStoryKit

/// One session row in the feed: provider mark, title, context line, origin
/// and machine badges, hover-revealed actions. The Granola card unit.
struct SessionCardView: View {
    let item: SessionItem
    var isLive = false
    var currentDeviceID: String? = nil
    var contextModel: AppModel? = nil
    var onOpen: () -> Void = {}
    var onResume: (() -> Void)? = nil

    @State private var hovering = false

    var body: some View {
        Button(action: onOpen) {
            cardBody
        }
        .buttonStyle(.tactileCard)
        .contextMenu {
            if let contextModel {
                OpenInMenuContent(model: contextModel, item: item)
            }
        }
        .accessibilityLabel("\(item.title), \(providerDisplayName)")
    }

    private var cardBody: some View {
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
    }

    private var providerMark: some View {
        ProviderIcon(provider: Provider(providerID: item.provider), fallbackName: providerDisplayName)
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

    /// Metadata owns the trailing edge; resume is a quiet affordance that
    /// only fills in on hover and never displaces the time or sync badge.
    private var trailing: some View {
        HStack(spacing: 10) {
            if let onResume, hovering {
                Button(action: onResume) {
                    HStack(spacing: 5) {
                        Image(systemName: crossMachine ? "arrow.down.on.square" : "arrow.uturn.forward")
                            .font(.system(size: 9, weight: .semibold))
                        Text(crossMachine ? "Resume here" : "Resume")
                            .font(Theme.body(10.5, weight: .medium))
                        if needsPro {
                            ProTag()
                        }
                    }
                    .foregroundStyle(Theme.accent)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(Theme.accent.opacity(0.08), in: Capsule())
                }
                .buttonStyle(.tactile)
                .transition(.opacity)
                .help(crossMachine
                    ? (needsPro ? "Pull this session onto this Mac with SpecStory Pro" : "Rebuild this session on this Mac and resume it")
                    : "Resume this session in its agent")
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

    /// Local-only is the default state; badge only what carries signal.
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
            EmptyView()
        }
    }

    private var crossMachine: Bool {
        item.isFromOtherMachine(currentDeviceID: currentDeviceID)
    }

    private var needsPro: Bool {
        guard let contextModel else { return false }
        return !contextModel.resumeEntitled(item)
    }

    private var providerDisplayName: String {
        Provider(providerID: item.provider)?.displayName ?? item.provider.capitalized
    }

    private var providerColor: Color {
        Provider(providerID: item.provider)?.badgeColor ?? Theme.inkSecondary
    }

    private static let relativeFormatter: RelativeDateTimeFormatter = {
        let formatter = RelativeDateTimeFormatter()
        formatter.unitsStyle = .abbreviated
        return formatter
    }()

    private var relativeTime: String {
        guard let date = item.updatedAt ?? item.createdAt else { return "" }
        if isLive || Date().timeIntervalSince(date) < 90 {
            return "now"
        }
        return Self.relativeFormatter.localizedString(for: date, relativeTo: Date())
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
