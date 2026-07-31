import SwiftUI

/// Status item label; icon state will reflect live/syncing/error in later phases.
struct MenuBarLabel: View {
    @ObservedObject var model: AppModel

    var body: some View {
        Image(systemName: "text.bubble")
            .accessibilityLabel("SpecStory")
            .help(model.menuBarHelp)
    }
}

/// Contents of the status item menu.
struct MenuBarContentView: View {
    @ObservedObject var model: AppModel
    let openMainWindow: () -> Void

    var body: some View {
        Button("Open SpecStory") {
            openMainWindow()
        }
        .keyboardShortcut("o")
        Divider()
        Button("Quit SpecStory") {
            model.quitApp()
        }
        .keyboardShortcut("q")
    }
}
