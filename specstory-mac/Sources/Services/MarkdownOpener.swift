import AppKit
import Foundation
import SpecStoryKit

/// A markdown-capable app installed on this Mac, shown with its real icon in
/// the Open in menu (the cloud web app's editor dropdown, natively).
struct EditorApp: Identifiable, Equatable {
    let name: String
    let bundleID: String
    let url: URL

    var id: String { bundleID }

    var icon: NSImage {
        let image = NSWorkspace.shared.icon(forFile: url.path)
        image.size = NSSize(width: 16, height: 16)
        return image
    }
}

/// Finds the session's markdown on disk (or materializes it) and opens it in
/// editors, Finder, or the pasteboard.
enum MarkdownOpener {
    /// Known editors in display order; only installed ones appear.
    private static let knownEditors: [(name: String, bundleID: String)] = [
        ("VS Code", "com.microsoft.VSCode"),
        ("VS Code Insiders", "com.microsoft.VSCodeInsiders"),
        ("Cursor", "com.todesktop.230313mzl4w4u92"),
        ("Zed", "dev.zed.Zed"),
        ("Sublime Text", "com.sublimetext.4"),
        ("BBEdit", "com.barebones.bbedit"),
        ("Nova", "com.panic.Nova"),
        ("Xcode", "com.apple.dt.Xcode"),
        ("TextEdit", "com.apple.TextEdit"),
    ]

    static func installedEditors() -> [EditorApp] {
        knownEditors.compactMap { editor in
            guard let url = NSWorkspace.shared.urlForApplication(withBundleIdentifier: editor.bundleID) else {
                return nil
            }
            return EditorApp(name: editor.name, bundleID: editor.bundleID, url: url)
        }
    }

    /// The session's markdown file in {project}/.specstory/history. Files are
    /// named by timestamp and slug, so match on the session id stamped inside
    /// (newest first, bounded) rather than guessing the name.
    static func historyFile(projectPath: String, sessionID: String) -> URL? {
        let historyDir = URL(fileURLWithPath: projectPath).appendingPathComponent(".specstory/history")
        guard let entries = try? FileManager.default.contentsOfDirectory(
            at: historyDir, includingPropertiesForKeys: [.contentModificationDateKey]
        ) else { return nil }

        let markdownFiles = entries
            .filter { $0.pathExtension == "md" }
            .sorted { lhs, rhs in
                let lDate = (try? lhs.resourceValues(forKeys: [.contentModificationDateKey]).contentModificationDate) ?? .distantPast
                let rDate = (try? rhs.resourceValues(forKeys: [.contentModificationDateKey]).contentModificationDate) ?? .distantPast
                return lDate > rDate
            }

        for file in markdownFiles.prefix(400) {
            // The generator stamps `<!-- <Provider> Session <id> ... -->` near
            // the top; reading a small head slice is enough.
            guard let handle = try? FileHandle(forReadingFrom: file) else { continue }
            defer { try? handle.close() }
            let head = (try? handle.read(upToCount: 4096)).flatMap { String(data: $0, encoding: .utf8) } ?? ""
            if head.contains(sessionID) {
                return file
            }
        }
        return nil
    }

    /// A real file for this session: the history file when the project is
    /// local, else the given markdown written to a temp file named after the
    /// session (cloud-only sessions).
    static func materializeFile(item: SessionItem, markdown: String?) -> URL? {
        if let projectPath = item.projectPath,
           let file = historyFile(projectPath: projectPath, sessionID: item.clientID) {
            return file
        }
        guard let markdown, !markdown.isEmpty else { return nil }
        let safeTitle = item.title
            .components(separatedBy: CharacterSet.alphanumerics.inverted)
            .filter { !$0.isEmpty }
            .joined(separator: "-")
            .prefix(60)
        let name = safeTitle.isEmpty ? item.clientID : String(safeTitle)
        let url = FileManager.default.temporaryDirectory
            .appendingPathComponent("SpecStory", isDirectory: true)
            .appendingPathComponent("\(name).md")
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        guard (try? markdown.write(to: url, atomically: true, encoding: .utf8)) != nil else { return nil }
        return url
    }

    static func open(_ file: URL, in editor: EditorApp) {
        NSWorkspace.shared.open(
            [file], withApplicationAt: editor.url,
            configuration: NSWorkspace.OpenConfiguration()
        )
    }

    static func revealInFinder(_ file: URL) {
        NSWorkspace.shared.activateFileViewerSelecting([file])
    }

    static func copyToPasteboard(_ text: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }
}
