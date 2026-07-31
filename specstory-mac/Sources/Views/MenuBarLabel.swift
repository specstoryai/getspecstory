import SwiftUI
import SpecStoryKit

/// Status item label; tint reflects app state.
struct MenuBarLabel: View {
    @ObservedObject var model: AppModel

    var body: some View {
        Image(systemName: symbolName)
            .accessibilityLabel("SpecStory")
            .help(model.menuBarHelp)
    }

    private var symbolName: String {
        switch model.menuBarState {
        case .live: return "text.bubble.fill"
        case .error: return "exclamationmark.bubble"
        case .signedOut, .idle, .syncing: return "text.bubble"
        }
    }
}

/// The status item menu: live sessions, recent sessions, quick actions.
struct MenuBarContentView: View {
    @ObservedObject var model: AppModel
    let openMainWindow: () -> Void

    var body: some View {
        Group {
            Text(model.statusText)

            if !model.liveSessionsOrdered.isEmpty {
                Divider()
                ForEach(model.liveSessionsOrdered.prefix(4)) { live in
                    Button {
                        model.revealSession(id: live.sessionID)
                        openMainWindow()
                    } label: {
                        let provider = Provider(providerID: live.provider)?.displayName ?? live.provider.capitalized
                        Text("● \(live.projectName): \(provider), \(live.promptCount) prompt\(live.promptCount == 1 ? "" : "s")")
                    }
                }
            }

            if !recentItems.isEmpty {
                Divider()
                Text("Recent")
                ForEach(recentItems) { item in
                    Button {
                        model.openSession(item)
                        openMainWindow()
                    } label: {
                        Text(menuTitle(for: item))
                    }
                }
            }

            Divider()
            Button("Open SpecStory") {
                openMainWindow()
            }
            .keyboardShortcut("o")
            Button("Search sessions") {
                openMainWindow()
                model.searchOverlayShown = true
            }
            .keyboardShortcut("k")

            Divider()
            Button("Quit SpecStory") {
                model.quitApp()
            }
            .keyboardShortcut("q")
        }
    }

    private var recentItems: [SessionItem] {
        Array(model.allItems.filter { model.liveSessions[$0.clientID] == nil }.prefix(5))
    }

    private func menuTitle(for item: SessionItem) -> String {
        var parts = [item.title]
        if let project = item.projectName {
            parts.append(project)
        }
        return parts.joined(separator: " · ")
    }
}
