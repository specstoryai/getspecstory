import SwiftUI
import SpecStoryKit

/// The Granola-style home feed: pinned Live now section, then date-grouped
/// session cards across every agent, project, and machine.
struct FeedView: View {
    @ObservedObject var model: AppModel

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 8, pinnedViews: []) {
                if let bootError = model.bootError {
                    ErrorBanner(text: bootError)
                }

                SyncStatusStrip(model: model)
                    .padding(.bottom, 10)

                if !model.liveSessionsOrdered.isEmpty {
                    liveSection
                        .padding(.bottom, 14)
                }

                if model.feedSections.isEmpty && model.liveSessionsOrdered.isEmpty {
                    emptyState
                        .padding(.top, 120)
                } else {
                    controlsRow
                    ForEach(model.feedSections) { section in
                        FeedSectionHeader(
                            title: section.title,
                            count: section.items.count,
                            collapsed: model.isSectionCollapsed(section)
                        ) {
                            model.toggleSection(section)
                        }
                        if !model.isSectionCollapsed(section) {
                            ForEach(section.items) { item in
                                SessionCardView(
                                    item: item,
                                    isLive: model.liveSessions[item.clientID] != nil,
                                    currentDeviceID: DeviceIdentity.current,
                                    contextModel: model,
                                    onOpen: { model.openSession(item) },
                                    onResume: model.canResume(item) ? { model.requestResume(item) } : nil
                                )
                            }
                        }
                    }
                }
            }
            .padding(.horizontal, 28)
            .padding(.top, 24)
            .padding(.bottom, 96)
            .frame(maxWidth: Theme.feedWidth)
            .frame(maxWidth: .infinity)
        }
        .background(Theme.paper)
        .task {
            await model.refreshFeed()
        }
    }

    private var liveSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Happening now")
                .font(Theme.display(22))
                .foregroundStyle(Theme.ink)
                .padding(.bottom, 4)
            ForEach(model.liveSessionsOrdered) { live in
                LiveSessionCard(model: model, live: live)
            }
        }
    }

    /// Quiet browse controls: group-by pills, sort menu on the right.
    private var controlsRow: some View {
        HStack(spacing: 8) {
            ForEach(FeedGrouping.allCases, id: \.self) { grouping in
                Button {
                    model.feedGrouping = grouping
                } label: {
                    Text(grouping.displayName)
                        .font(Theme.body(11, weight: model.feedGrouping == grouping ? .semibold : .regular))
                        .foregroundStyle(model.feedGrouping == grouping ? Theme.ink : Theme.inkSecondary)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 5)
                        .background(
                            model.feedGrouping == grouping ? Theme.sidebarSelection : Color.clear,
                            in: Capsule()
                        )
                }
                .buttonStyle(.tactile)
            }

            Spacer()

            Button {
                model.expandAllSections()
            } label: {
                Image(systemName: "rectangle.expand.vertical")
                    .font(.system(size: 10))
                    .foregroundStyle(Theme.inkSecondary)
                    .frame(width: 22, height: 22)
            }
            .buttonStyle(.tactile)
            .help("Expand all sections")
            .accessibilityLabel("Expand all sections")

            Button {
                model.collapseAllSections()
            } label: {
                Image(systemName: "rectangle.compress.vertical")
                    .font(.system(size: 10))
                    .foregroundStyle(Theme.inkSecondary)
                    .frame(width: 22, height: 22)
            }
            .buttonStyle(.tactile)
            .help("Collapse all sections")
            .accessibilityLabel("Collapse all sections")

            Menu {
                ForEach(FeedSort.allCases, id: \.self) { sort in
                    Button {
                        model.feedSort = sort
                    } label: {
                        if model.feedSort == sort {
                            Label(sort.displayName, systemImage: "checkmark")
                        } else {
                            Text(sort.displayName)
                        }
                    }
                }
            } label: {
                HStack(spacing: 4) {
                    Image(systemName: "arrow.up.arrow.down")
                        .font(.system(size: 10))
                    Text(model.feedSort.displayName)
                        .font(Theme.body(11))
                }
                .foregroundStyle(Theme.inkSecondary)
            }
            .menuStyle(.borderlessButton)
            .fixedSize()
            .accessibilityLabel("Sort sessions")
        }
        .padding(.bottom, 6)
    }


    private var emptyState: some View {
        VStack(spacing: 10) {
            Image(systemName: "text.bubble")
                .font(.system(size: 34, weight: .light))
                .foregroundStyle(Theme.inkTertiary)
            Text("Your sessions will appear here")
                .font(Theme.display(20))
                .foregroundStyle(Theme.ink)
            Text("Start a session in Claude Code, Cursor, Codex, Gemini, or any supported agent and SpecStory picks it up automatically.")
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 380)
        }
        .frame(maxWidth: .infinity)
    }
}

/// A live session gets a warmer, more prominent card.
struct LiveSessionCard: View {
    @ObservedObject var model: AppModel
    let live: LiveSession

    @State private var hovering = false

    var body: some View {
        Button {
            model.revealSession(id: live.sessionID)
        } label: {
            cardBody
        }
        .buttonStyle(.tactileCard)
    }

    private var cardBody: some View {
        HStack(spacing: 12) {
            RoundedRectangle(cornerRadius: 2)
                .fill(Theme.live)
                .frame(width: 3, height: 34)

            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 8) {
                    Text(live.projectName)
                        .font(Theme.body(13, weight: .semibold))
                        .foregroundStyle(Theme.ink)
                    LivePill()
                }
                HStack(spacing: 5) {
                    let provider = Provider(providerID: live.provider)
                    Text(provider?.displayName ?? live.provider.capitalized)
                        .foregroundStyle(provider?.badgeColor ?? Theme.inkSecondary)
                    Text("·").foregroundStyle(Theme.inkTertiary)
                    Text("\(live.promptCount) prompt\(live.promptCount == 1 ? "" : "s")")
                    Text("·").foregroundStyle(Theme.inkTertiary)
                    Text(syncLabel)
                }
                .font(Theme.body(11))
                .foregroundStyle(Theme.inkSecondary)
            }

            Spacer()

            Text(durationLabel)
                .font(Theme.body(11))
                .foregroundStyle(Theme.inkTertiary)
                .monospacedDigit()
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
        .cardChrome(hovering: hovering)
        .onHover { hovering = $0 }
        .contentShape(RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous))
    }

    private var syncLabel: String {
        if case .signedIn = model.authState, model.cloudSyncEnabled {
            return "Syncing to Cloud"
        }
        return "Saving locally"
    }

    private var durationLabel: String {
        let minutes = max(0, Int(Date().timeIntervalSince(live.startedAt)) / 60)
        if minutes < 1 { return "just started" }
        if minutes < 60 { return "\(minutes)m" }
        return "\(minutes / 60)h \(minutes % 60)m"
    }
}

struct ErrorBanner: View {
    let text: String

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: "exclamationmark.triangle")
                .foregroundStyle(.orange)
            Text(text)
                .font(Theme.body(12))
                .foregroundStyle(Theme.ink)
            Spacer()
        }
        .padding(12)
        .background(Color.orange.opacity(0.08), in: RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.cardRadius, style: .continuous).strokeBorder(Color.orange.opacity(0.25)))
    }
}

/// A tappable feed section header: chevron, uppercase title, session count.
struct FeedSectionHeader: View {
    let title: String
    let count: Int
    let collapsed: Bool
    let toggle: () -> Void

    @State private var hovering = false

    var body: some View {
        Button(action: toggle) {
            HStack(spacing: 6) {
                Image(systemName: "chevron.down")
                    .font(.system(size: 9, weight: .semibold))
                    .foregroundStyle(Theme.inkTertiary)
                    .rotationEffect(.degrees(collapsed ? -90 : 0))
                Text(title)
                    .font(Theme.body(12, weight: .semibold))
                    .foregroundStyle(hovering ? Theme.inkSecondary : Theme.inkTertiary)
                    .textCase(.uppercase)
                    .kerning(0.4)
                Text("\(count)")
                    .font(Theme.body(10, weight: .medium))
                    .foregroundStyle(Theme.inkTertiary)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 1)
                    .background(Theme.sidebarSelection, in: Capsule())
                Spacer()
            }
            .padding(.top, 14)
            .padding(.bottom, 2)
            .contentShape(Rectangle())
        }
        .buttonStyle(.tactile)
        .onHover { hovering = $0 }
        .animation(.easeOut(duration: 0.15), value: collapsed)
        .accessibilityLabel("\(title), \(count) sessions, \(collapsed ? "collapsed" : "expanded")")
    }
}

/// The Home heads-up strip: session totals, sync coverage, live activity,
/// and cloud connectivity as a quiet one-line readout.
struct SyncStatusStrip: View {
    @ObservedObject var model: AppModel

    var body: some View {
        HStack(spacing: 6) {
            connectivityDot
            Text(connectivityLabel)
                .foregroundStyle(connectivityColor)
            if settling {
                ProgressView()
                    .controlSize(.mini)
                    .padding(.leading, 2)
            }

            if let total = model.localTotalSessions {
                divider
                Text("\(total.formatted()) sessions")
            }

            if model.signedInEmail != nil, model.cloudReachable == true, let synced = model.cloudSyncedTotal {
                divider
                Text("\(synced.formatted()) synced")
                if let bytes = model.cloudSyncedBytes {
                    Text("(\(ByteCountFormatter.string(fromByteCount: Int64(bytes), countStyle: .file)))")
                }
            }

            if let others = model.otherMachineTotal, others > 0 {
                divider
                Text("\(others.formatted()) from other machines")
            }

            if !model.liveSessions.isEmpty {
                divider
                HStack(spacing: 4) {
                    Circle().fill(Theme.live).frame(width: 5, height: 5)
                    Text("\(model.liveSessions.count) syncing now")
                        .foregroundStyle(Theme.live)
                }
            }

            Spacer()
        }
        .font(Theme.body(11))
        .foregroundStyle(Theme.inkTertiary)
        .accessibilityElement(children: .combine)
    }

    /// Facts still resolving: local count pending, or the first cloud round
    /// trip has not settled while signed in.
    private var settling: Bool {
        if model.localTotalSessions == nil { return true }
        if model.signedInEmail != nil, model.cloudReachable == nil { return true }
        if model.signedInEmail != nil, model.cloudReachable == true, model.cloudSyncedTotal == nil { return true }
        return false
    }

    private var divider: some View {
        Text("·").foregroundStyle(Theme.inkTertiary.opacity(0.6))
    }

    @ViewBuilder private var connectivityDot: some View {
        Circle()
            .fill(dotColor)
            .frame(width: 6, height: 6)
    }

    private var dotColor: Color {
        guard model.signedInEmail != nil else { return Theme.inkTertiary }
        switch model.cloudReachable {
        case .some(true): return Theme.synced
        case .some(false): return Color.orange
        case .none: return Theme.inkTertiary
        }
    }

    private var connectivityColor: Color {
        model.cloudReachable == false ? Color.orange : Theme.inkSecondary
    }

    private var connectivityLabel: String {
        guard model.signedInEmail != nil else { return "Local only" }
        switch model.cloudReachable {
        case .some(true): return "Cloud connected"
        case .some(false): return "Reconnecting to Cloud"
        case .none: return "Connecting to Cloud"
        }
    }
}
