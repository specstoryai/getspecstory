import SwiftUI
import SpecStoryKit

/// The branded session log viewer, ported from the SpecStory Cloud web
/// experience: sticky header, element filter row, exchange cards (prompt
/// bubble + agent output), and a numbered jump list at wide widths.
struct SessionDetailView: View {
    @ObservedObject var model: AppModel
    let item: SessionItem

    @StateObject private var state = TranscriptState()

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider().overlay(Theme.hairline)
            contentBody
        }
        .background(Theme.paper)
        .onAppear { state.update(markdown: model.sessionMarkdown) }
        .onChange(of: model.sessionMarkdown) { _, markdown in
            state.update(markdown: markdown)
        }
    }

    // MARK: Header (sticky above the scroll)

    private var header: some View {
        VStack(alignment: .leading, spacing: 10) {
            Button {
                model.closeSession()
            } label: {
                Label("Back", systemImage: "chevron.left")
                    .font(Theme.body(12, weight: .medium))
                    .foregroundStyle(Theme.inkSecondary)
            }
            .buttonStyle(.plain)

            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 6) {
                    Text(item.title)
                        .font(Theme.display(24))
                        .foregroundStyle(Theme.ink)
                        .lineLimit(2)
                    HStack(spacing: 6) {
                        let provider = Provider(providerID: item.provider)
                        Text(provider?.displayName ?? item.provider.capitalized)
                            .foregroundStyle(provider?.badgeColor ?? Theme.inkSecondary)
                        if let project = item.projectName {
                            Text("·").foregroundStyle(Theme.inkTertiary)
                            Text(project)
                        }
                        if let machine = item.machineName, item.isFromOtherMachine(currentDeviceID: DeviceIdentity.current) {
                            Text("·").foregroundStyle(Theme.inkTertiary)
                            Label(machine, systemImage: "laptopcomputer")
                        }
                        if let date = item.updatedAt ?? item.createdAt {
                            Text("·").foregroundStyle(Theme.inkTertiary)
                            Text(date.formatted(date: .abbreviated, time: .shortened))
                        }
                        if let transcript = state.transcript, !transcript.exchanges.isEmpty {
                            Text("·").foregroundStyle(Theme.inkTertiary)
                            Text("\(transcript.exchanges.count) prompt\(transcript.exchanges.count == 1 ? "" : "s")")
                                .monospacedDigit()
                        }
                    }
                    .font(Theme.body(12))
                    .foregroundStyle(Theme.inkSecondary)
                }
                Spacer()
                HStack(spacing: 8) {
                    Menu {
                        OpenInMenuContent(model: model, item: item)
                    } label: {
                        Image(systemName: "square.and.arrow.up")
                            .font(.system(size: 12))
                    }
                    .menuStyle(.borderlessButton)
                    .fixedSize()
                    .help("Open, reveal, or copy this session's markdown")
                    .accessibilityLabel("Open session markdown elsewhere")
                    if let projectID = item.projectID {
                        Link(destination: AppModel.cloudBaseURL.appendingPathComponent("projects/\(projectID)/sessions/\(item.clientID)")) {
                            Label("Open in Cloud", systemImage: "safari")
                                .font(Theme.body(12))
                        }
                        .buttonStyle(.bordered)
                    }
                    if model.canResume(item) {
                        Button {
                            model.requestResume(item)
                        } label: {
                            Label("Resume", systemImage: "arrow.uturn.forward")
                                .font(Theme.body(12, weight: .medium))
                        }
                        .buttonStyle(.borderedProminent)
                        .tint(Theme.accent)
                    }
                }
            }
        }
        .padding(.horizontal, 28)
        .padding(.top, 18)
        .padding(.bottom, 14)
    }

    // MARK: Content

    @ViewBuilder private var contentBody: some View {
        if model.detailLoading || state.parsing {
            VStack {
                Spacer()
                ProgressView("Loading session")
                    .controlSize(.small)
                Spacer()
            }
            .frame(maxWidth: .infinity)
        } else if let transcript = state.transcript, !transcript.exchanges.isEmpty {
            TranscriptContentView(state: state, transcript: transcript, sessionID: item.clientID)
        } else if let markdown = model.sessionMarkdown {
            // Not SpecStory-shaped markdown: render it plainly rather than
            // showing nothing.
            RawMarkdownFallbackView(markdown: markdown)
        } else {
            VStack(spacing: 8) {
                Spacer()
                Image(systemName: "doc.text")
                    .font(.system(size: 28, weight: .light))
                    .foregroundStyle(Theme.inkTertiary)
                Text("This session's content is not available here yet")
                    .font(Theme.body(13, weight: .medium))
                    .foregroundStyle(Theme.ink)
                Text(unavailableReason)
                    .font(Theme.body(12))
                    .foregroundStyle(Theme.inkSecondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 380)
                Spacer()
            }
            .frame(maxWidth: .infinity)
        }
    }

    private var unavailableReason: String {
        if item.origin == .cloudOnly, model.signedInEmail == nil {
            return "Sign in to SpecStory Cloud to read sessions synced from your machines."
        }
        return "The session may still be syncing, or its project folder is not reachable on this Mac."
    }
}

// MARK: - Transcript layout

/// Filter bar + scrolling exchange feed, with the jump list as a right rail
/// at wide widths and a popover behind a toolbar button when narrow.
private struct TranscriptContentView: View {
    @ObservedObject var state: TranscriptState
    let transcript: SessionTranscript
    let sessionID: String

    @State private var tocPopoverShown = false

    /// Below this width the TOC rail folds into a popover.
    private static let wideThreshold: CGFloat = 940

    var body: some View {
        GeometryReader { geometry in
            let wide = geometry.size.width >= Self.wideThreshold
            ScrollViewReader { proxy in
                VStack(spacing: 0) {
                    HStack(spacing: 0) {
                        ElementFilterRow(state: state)
                        if !wide {
                            tocButton(proxy: proxy)
                                .padding(.trailing, 14)
                        }
                    }
                    .background(Theme.paper)

                    Divider().overlay(Theme.hairline)

                    HStack(spacing: 0) {
                        transcriptScroll
                        if wide {
                            Divider().overlay(Theme.hairline)
                            TranscriptTOCList(
                                exchanges: transcript.exchanges,
                                activeID: state.activeExchangeID
                            ) { id in
                                jump(to: id, proxy: proxy)
                            }
                            .frame(minWidth: 200, idealWidth: 240, maxWidth: 260)
                        }
                    }
                }
            }
        }
    }

    private func tocButton(proxy: ScrollViewProxy) -> some View {
        Button {
            tocPopoverShown = true
        } label: {
            Image(systemName: "list.number")
                .font(.system(size: 12, weight: .medium))
                .foregroundStyle(Theme.inkSecondary)
                .frame(width: 24, height: 24)
                .background(Theme.card, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 6, style: .continuous).strokeBorder(Theme.hairline))
        }
        .buttonStyle(.plain)
        .help("Jump to a prompt")
        .popover(isPresented: $tocPopoverShown, arrowEdge: .bottom) {
            TranscriptTOCList(
                exchanges: transcript.exchanges,
                activeID: state.activeExchangeID
            ) { id in
                tocPopoverShown = false
                jump(to: id, proxy: proxy)
            }
            .frame(width: 280, height: 400)
        }
    }

    private var transcriptScroll: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 18) {
                ForEach(transcript.exchanges) { exchange in
                    ExchangeCardView(
                        exchange: exchange,
                        number: exchange.id + 1,
                        visible: state.visible
                    )
                    .equatable()
                    .id(exchange.id)
                    .background(
                        GeometryReader { cardGeometry in
                            Color.clear.preference(
                                key: ExchangeTopPreferenceKey.self,
                                value: [exchange.id: cardGeometry.frame(in: .named("transcriptScroll")).minY]
                            )
                        }
                    )
                }
            }
            // Fresh per-session identity so expansion state never bleeds
            // between sessions that share exchange ids.
            .id(sessionID)
            .padding(.horizontal, 28)
            .padding(.vertical, 20)
            .frame(maxWidth: 780, alignment: .leading)
            .frame(maxWidth: .infinity)
        }
        .coordinateSpace(name: "transcriptScroll")
        .onPreferenceChange(ExchangeTopPreferenceKey.self) { tops in
            updateActive(tops)
        }
    }

    private func jump(to id: Int, proxy: ScrollViewProxy) {
        state.activeExchangeID = id
        proxy.scrollTo(id, anchor: .top)
    }

    /// Active exchange = the card nearest above the reading line (cloud uses
    /// the top 10% of the viewport; a fixed offset reads the same here).
    private func updateActive(_ tops: [Int: CGFloat]) {
        guard !tops.isEmpty else { return }
        let threshold: CGFloat = 120
        let active = tops.filter { $0.value <= threshold }.max { $0.value < $1.value }?.key
            ?? tops.min { $0.value < $1.value }?.key
        if let active, state.activeExchangeID != active {
            state.activeExchangeID = active
        }
    }
}

private struct ExchangeTopPreferenceKey: PreferenceKey {
    static var defaultValue: [Int: CGFloat] = [:]

    static func reduce(value: inout [Int: CGFloat], nextValue: () -> [Int: CGFloat]) {
        value.merge(nextValue()) { _, new in new }
    }
}

// MARK: - Fallback for non-SpecStory markdown

struct RawMarkdownFallbackView: View {
    private let segments: [SessionTranscript.Segment]

    init(markdown: String) {
        segments = SessionTranscript.fenceSegments(in: markdown)
    }

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 10) {
                ForEach(Array(segments.enumerated()), id: \.offset) { _, segment in
                    switch segment {
                    case .prose(let text):
                        ProseBlocksView(text: text)
                    case .code(let language, let filename, let content):
                        CodeBlockView(language: language, filename: filename, content: content)
                    default:
                        EmptyView()
                    }
                }
            }
            .padding(.horizontal, 28)
            .padding(.vertical, 20)
            .frame(maxWidth: 780, alignment: .leading)
            .frame(maxWidth: .infinity)
        }
    }
}
