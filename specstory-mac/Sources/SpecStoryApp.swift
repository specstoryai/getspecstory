import AppKit
import Foundation
import SwiftUI

/// Manages the main window, handles reopen events, and flips the activation
/// policy so the Dock icon appears only while the desktop window is open
/// (Granola behavior for an LSUIElement app).
final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    var mainWindow: NSWindow?

    func applicationDidFinishLaunching(_ notification: Notification) {
        guard let model = AppModel.shared else { return }
        Task { await model.bootstrap() }
        let contentView = MainWindowView(model: model)
        let hostingController = NSHostingController(rootView: contentView)

        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1080, height: 720),
            styleMask: [.titled, .closable, .resizable, .miniaturizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        window.contentViewController = hostingController
        window.title = "SpecStory"
        window.titlebarAppearsTransparent = true
        window.titleVisibility = .hidden
        window.isReleasedWhenClosed = false
        window.minSize = NSSize(width: 760, height: 480)
        window.delegate = self
        window.setFrameAutosaveName("SpecStoryMainWindow")
        if !window.setFrameUsingName("SpecStoryMainWindow") {
            window.center()
        }
        mainWindow = window

        showMainWindow()
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        showMainWindow()
        return true
    }

    func showMainWindow() {
        NSApp.setActivationPolicy(.regular)
        mainWindow?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    func windowWillClose(_ notification: Notification) {
        // Back to a pure menu bar presence when the desktop window closes.
        NSApp.setActivationPolicy(.accessory)
    }
}

@main
struct SpecStoryApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @StateObject private var model = AppModel()

    var body: some Scene {
        MenuBarExtra {
            MenuBarContentView(model: model) {
                appDelegate.showMainWindow()
            }
        } label: {
            MenuBarLabel(model: model)
        }
        .menuBarExtraStyle(.menu)
    }
}
