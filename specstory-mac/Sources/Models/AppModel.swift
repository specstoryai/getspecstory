import Foundation
import SwiftUI

/// Navigation destinations for the desktop window sidebar.
enum PanelMode: String, CaseIterable, Identifiable {
    case home
    case cloud
    case chat
    case providers
    case settings

    var id: String { rawValue }
}

/// The app's single source of truth (Rook's god-model pattern): all mutation
/// happens here on the main actor; services stay dumb and report back through
/// closures.
@MainActor
final class AppModel: ObservableObject {
    static private(set) weak var shared: AppModel?

    @Published var panelMode: PanelMode = .home
    @Published var statusText = "Idle"

    var menuBarHelp: String { "SpecStory: \(statusText)" }

    init() {
        AppModel.shared = self
    }

    func quitApp() {
        NSApplication.shared.terminate(nil)
    }
}
