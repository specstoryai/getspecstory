import SwiftUI
import SpecStoryKit

/// ⌘K: dimmed backdrop with a centered search panel over local FTS merged
/// with cloud search.
struct SearchOverlay: View {
    @ObservedObject var model: AppModel
    @FocusState private var focused: Bool

    var body: some View {
        ZStack(alignment: .top) {
            Color.black.opacity(0.25)
                .ignoresSafeArea()
                .onTapGesture { dismiss() }

            VStack(spacing: 0) {
                HStack(spacing: 10) {
                    Image(systemName: "magnifyingglass")
                        .foregroundStyle(Theme.inkTertiary)
                    TextField("Search sessions across every agent and machine", text: $model.searchQuery)
                        .textFieldStyle(.plain)
                        .font(Theme.body(15))
                        .focused($focused)
                        .onChange(of: model.searchQuery) { _ in model.searchQueryChanged() }
                        .onSubmit {
                            if let first = model.searchResults.first {
                                open(first)
                            }
                        }
                    if !model.searchQuery.isEmpty {
                        Button {
                            model.searchQuery = ""
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

                if !model.searchResults.isEmpty {
                    Divider().overlay(Theme.hairline)
                    ScrollView {
                        LazyVStack(spacing: 4) {
                            ForEach(model.searchResults) { item in
                                SessionCardView(
                                    item: item,
                                    isLive: model.liveSessions[item.clientID] != nil,
                                    currentDeviceID: DeviceIdentity.current,
                                    contextModel: model,
                                    onOpen: { open(item) },
                                    onResume: model.canResume(item) ? { model.requestResume(item) } : nil
                                )
                            }
                        }
                        .padding(10)
                    }
                    .frame(maxHeight: 380)
                } else if !model.searchQuery.isEmpty {
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
