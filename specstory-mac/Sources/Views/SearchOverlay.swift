import SwiftUI
import SpecStoryKit

/// ⌘K: dimmed backdrop with a centered search panel over local FTS merged
/// with cloud search.
struct SearchOverlay: View {
    @ObservedObject var model: AppModel
    @ObservedObject var mention: MentionState
    @FocusState private var focused: Bool

    init(model: AppModel) {
        self.model = model
        self.mention = model.searchMention
    }

    private func candidatesProvider() -> [MentionItem] {
        mention.candidatesFromApp(cloudProjects: model.cloudProjects)
    }

    var body: some View {
        ZStack(alignment: .top) {
            Color.black.opacity(0.25)
                .ignoresSafeArea()
                .onTapGesture { dismiss() }

            VStack(spacing: 0) {
                HStack(spacing: 10) {
                    Image(systemName: "magnifyingglass")
                        .foregroundStyle(Theme.inkTertiary)
                    TextField("Search sessions, @ to filter by project, agent, or time", text: $mention.text)
                        .textFieldStyle(.plain)
                        .font(Theme.body(16))
                        .focused($focused)
                        .mentionTextFieldSupport(mention, candidates: candidatesProvider)
                        .onChange(of: mention.text) { _ in model.searchQueryChanged() }
                        .onChange(of: mention.selectedProjectIDs) { _ in model.searchQueryChanged() }
                        .onChange(of: mention.selectedAgents) { _ in model.searchQueryChanged() }
                        .onChange(of: mention.timeFilter) { _ in model.searchQueryChanged() }
                        .onSubmit {
                            if mention.consumeReturn(candidates: candidatesProvider()) { return }
                            if let first = model.searchResults.first {
                                open(first.item)
                            }
                        }
                    if model.searchingLocal || model.searchingCloud {
                        ProgressView()
                            .controlSize(.small)
                    }
                    if !mention.text.isEmpty || mention.hasChips {
                        Button {
                            mention.text = ""
                            mention.clearAll()
                            model.searchResults = []
                        } label: {
                            Image(systemName: "xmark.circle.fill")
                                .foregroundStyle(Theme.inkTertiary)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Clear search")
                    }
                }
                .padding(16)

                if mention.hasChips {
                    MentionChipRow(state: mention)
                        .padding(.horizontal, 16)
                        .padding(.bottom, 10)
                }

                if mention.popoverShown {
                    MentionPanel(model: model, state: mention)
                        .padding(.horizontal, 12)
                        .padding(.bottom, 12)
                }

                Divider().overlay(Theme.hairline)

                if !model.queryReady(mention.text) && !mention.hasChips {
                    // Resting state: the palette is alive before a query.
                    resultsList(model.recentSearchRows, header: "Recent")
                } else if !model.searchResults.isEmpty {
                    resultsList(model.searchResults, header: nil)
                } else if model.searchingLocal || model.searchingCloud {
                    HStack(spacing: 8) {
                        ProgressView().controlSize(.small)
                        Text("Searching your sessions")
                            .font(Theme.body(12))
                            .foregroundStyle(Theme.inkSecondary)
                    }
                    .padding(18)
                } else {
                    Text("No sessions match")
                        .font(Theme.body(12))
                        .foregroundStyle(Theme.inkTertiary)
                        .padding(18)
                }

                Divider().overlay(Theme.hairline)
                hintBar
            }
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).strokeBorder(Theme.hairline))
            .shadow(color: .black.opacity(0.25), radius: 30, y: 10)
            .frame(maxWidth: 820)
            .padding(.top, 56)
            .padding(.horizontal, 40)
        }
        .onAppear { focused = true }
        .transition(.opacity)
    }

    @ViewBuilder private func resultsList(_ rows: [SearchResultRow], header: String?) -> some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 4) {
                if let header {
                    Text(header)
                        .font(Theme.body(10, weight: .semibold))
                        .foregroundStyle(Theme.inkTertiary)
                        .textCase(.uppercase)
                        .kerning(0.4)
                        .padding(.horizontal, 8)
                        .padding(.top, 4)
                }
                ForEach(rows) { row in
                    VStack(alignment: .leading, spacing: 0) {
                        SessionCardView(
                            item: row.item,
                            isLive: model.liveSessions[row.item.clientID] != nil,
                            currentDeviceID: DeviceIdentity.current,
                            contextModel: model,
                            onOpen: { open(row.item) },
                            onResume: model.canResume(row.item) ? { model.requestResume(row.item) } : nil
                        )
                        if let snippet = row.snippet {
                            SnippetLineView(snippet: snippet)
                                .padding(.horizontal, 16)
                                .padding(.top, 4)
                                .padding(.bottom, 6)
                        }
                    }
                }
                if model.searchingCloud && header == nil {
                    HStack(spacing: 6) {
                        ProgressView().controlSize(.mini)
                        Text("Searching Cloud")
                            .font(Theme.body(11))
                            .foregroundStyle(Theme.inkTertiary)
                    }
                    .padding(.horizontal, 8)
                    .padding(.vertical, 6)
                }
            }
            .padding(10)
        }
        .frame(maxHeight: 560)
    }

    /// The palette legend: what the keys do, always visible.
    private var hintBar: some View {
        HStack(spacing: 14) {
            hint(key: "return", text: "open first result")
            hint(key: "@", text: "filter by project, agent, or time")
            hint(key: "esc", text: "close")
            Spacer()
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
    }

    private func hint(key: String, text: String) -> some View {
        HStack(spacing: 4) {
            Text(key)
                .font(Theme.mono(9))
                .foregroundStyle(Theme.inkSecondary)
                .padding(.horizontal, 5)
                .padding(.vertical, 2)
                .background(Theme.sidebarSelection, in: RoundedRectangle(cornerRadius: 4, style: .continuous))
            Text(text)
                .font(Theme.body(10))
                .foregroundStyle(Theme.inkTertiary)
        }
    }

    private func open(_ item: SessionItem) {
        dismiss()
        model.panelMode = .home
        model.openSession(item)
    }

    private func dismiss() {
        model.dismissSearchOverlay()
    }
}
