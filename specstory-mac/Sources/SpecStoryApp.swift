import AppKit
import Foundation
import SwiftUI

/// Manages the main window, handles reopen events, and flips the activation
/// policy so the Dock icon appears only while the desktop window is open
/// (Granola behavior for an LSUIElement app).
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    var mainWindow: NSWindow?
    private var terminationPrepared = false

    func applicationDidFinishLaunching(_ notification: Notification) {
        // Debug-only hooks for automated visual verification.
        let env = ProcessInfo.processInfo.environment
        if env["SPECSTORY_DEBUG_APPEARANCE"] == "dark" {
            NSApp.appearance = NSAppearance(named: .darkAqua)
        }
        // The SwiftUI scene may not have created the model yet; open the
        // window on the next runloop turn once it exists.
        DispatchQueue.main.async { [weak self] in
            self?.showMainWindow()
            if let panel = env["SPECSTORY_DEBUG_PANEL"].flatMap(PanelMode.init(rawValue:)) {
                DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
                    AppModel.shared?.panelMode = panel
                }
            }
            if env["SPECSTORY_DEBUG_SEARCH"] == "1" {
                DispatchQueue.main.asyncAfter(deadline: .now() + 8) {
                    AppModel.shared?.searchOverlayShown = true
                }
            }
            if env["SPECSTORY_DEBUG_OPEN_RECENT"] == "1" {
                // Poll until the feed has content, then open the newest session.
                func tryOpen(attempt: Int) {
                    guard attempt < 20 else { return }
                    DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
                        guard let model = AppModel.shared else { return }
                        if let first = model.allItems.first, model.selectedSession == nil {
                            model.openSession(first)
                        } else if model.allItems.isEmpty {
                            tryOpen(attempt: attempt + 1)
                        }
                    }
                }
                tryOpen(attempt: 0)
            }
        }
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        showMainWindow()
        return true
    }

    /// Deliver SIGTERM to the watch fleet and give children a brief head
    /// start on their cloud flush before the process (and their pipes) go
    /// away. Without this, terminate() outruns signal delivery and orphans
    /// the fleet.
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard let model = AppModel.shared, model.supervisor != nil, !terminationPrepared else {
            return .terminateNow
        }
        terminationPrepared = true
        model.supervisor?.stopAll(patience: 20)
        model.tripwire?.stop()
        DispatchQueue.global().asyncAfter(deadline: .now() + 1.5) {
            DispatchQueue.main.async {
                NSApp.reply(toApplicationShouldTerminate: true)
            }
        }
        return .terminateLater
    }

    /// Builds the window lazily so it never depends on launch ordering.
    private func makeWindowIfNeeded() {
        guard mainWindow == nil, let model = AppModel.shared else { return }
        let hostingController = NSHostingController(rootView: MainWindowView(model: model))

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
    }

    func showMainWindow() {
        makeWindowIfNeeded()
        guard let mainWindow else { return }
        NSApp.setActivationPolicy(.regular)
        // An LSUIElement app that flips to .regular gets the generic Dock
        // tile unless the icon is set at runtime.
        NSApp.applicationIconImage = NSImage(named: "AppIcon")
        mainWindow.makeKeyAndOrderFront(nil)
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
