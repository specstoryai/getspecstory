import SwiftUI

/// Granola-style shell: sidebar plus content. Fleshed out in the UI phases;
/// this skeleton proves the window plumbing.
struct MainWindowView: View {
    @ObservedObject var model: AppModel

    var body: some View {
        NavigationSplitView {
            List(selection: $model.panelMode) {
                Label("Home", systemImage: "house").tag(PanelMode.home)
                Label("Cloud", systemImage: "icloud").tag(PanelMode.cloud)
                Label("Chat", systemImage: "sparkles").tag(PanelMode.chat)
                Label("Providers", systemImage: "cpu").tag(PanelMode.providers)
                Label("Settings", systemImage: "gearshape").tag(PanelMode.settings)
            }
            .navigationSplitViewColumnWidth(min: 200, ideal: 220)
        } detail: {
            switch model.panelMode {
            case .home:
                ContentUnavailableView(
                    "Your sessions will appear here",
                    systemImage: "text.bubble",
                    description: Text("SpecStory watches your AI coding sessions across every agent.")
                )
            case .cloud:
                ContentUnavailableView("Cloud", systemImage: "icloud")
            case .chat:
                ContentUnavailableView("Ask anything", systemImage: "sparkles")
            case .providers:
                ContentUnavailableView("Providers", systemImage: "cpu")
            case .settings:
                ContentUnavailableView("Settings", systemImage: "gearshape")
            }
        }
        .frame(minWidth: 760, minHeight: 480)
    }
}
