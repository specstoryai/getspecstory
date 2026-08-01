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
                        .font(Theme.body(15))
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

                if !model.searchResults.isEmpty {
                    Divider().overlay(Theme.hairline)
                    ScrollView {
                        LazyVStack(spacing: 4) {
                            ForEach(model.searchResults) { row in
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
                        }
                        .padding(10)
                    }
                    .frame(maxHeight: 380)
                } else if !mention.text.isEmpty || mention.hasChips {
                    Divider().overlay(Theme.hairline)
                    Text("No sessions match yet")
                        .font(Theme.body(12))
                        .foregroundStyle(Theme.inkTertiary)
                        .padding(18)
                }
            }
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).strokeBorder(Theme.hairline))
            .shadow(color: .black.opacity(0.25), radius: 30, y: 10)
            .frame(maxWidth: 640)
            .padding(.top, 90)
            .padding(.horizontal, 40)
        }
        .onAppear { focused = true }
        .transition(.opacity)
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
