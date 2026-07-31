import Foundation
import SpecStoryKit

extension AppModel {
    // MARK: Open markdown outside the app

    var installedEditors: [EditorApp] {
        MarkdownOpener.installedEditors()
    }

    /// A file suitable for opening externally. Local sessions resolve to
    /// their .specstory/history file; cloud-only sessions materialize the
    /// loaded markdown into a temp file.
    private func externalFile(for item: SessionItem) async -> URL? {
        var markdown = markdownCache[item.clientID]?.markdown
        if markdown == nil, selectedSession?.clientID == item.clientID {
            markdown = sessionMarkdown
        }
        if let file = MarkdownOpener.materializeFile(item: item, markdown: markdown) {
            return file
        }
        // Nothing cached yet (card context menu on an unopened session):
        // load the markdown the same way the detail view would.
        await loadMarkdownIntoCache(for: item)
        return MarkdownOpener.materializeFile(item: item, markdown: markdownCache[item.clientID]?.markdown)
    }

    /// Detail-view loads populate sessionMarkdown; this one only fills the
    /// cache so background opens do not disturb the visible selection.
    private func loadMarkdownIntoCache(for item: SessionItem) async {
        if item.origin != .cloudOnly, let projectPath = item.projectPath, let binaryURL {
            let output = try? await CLIRunner.run(
                binary: binaryURL,
                arguments: ["sync", "-s", item.clientID, "--print", "--no-cloud-sync", "--silent"],
                workingDirectory: projectPath, environment: [:], timeout: 60
            )
            if let output, !output.isEmpty {
                markdownCache[item.clientID] = (nil, output)
                return
            }
        }
        if let projectID = item.projectID, case .signedIn = authState {
            let result = try? await auth.api.sessionMarkdown(projectID: projectID, sessionID: item.clientID, etag: nil)
            if case .content(let markdown, let etag) = result {
                markdownCache[item.clientID] = (etag, markdown)
            }
        }
    }

    func openMarkdown(_ item: SessionItem, in editor: EditorApp) {
        Task {
            guard let file = await externalFile(for: item) else {
                showToast("This session's markdown is not available yet")
                return
            }
            MarkdownOpener.open(file, in: editor)
        }
    }

    func revealMarkdownInFinder(_ item: SessionItem) {
        Task {
            guard let file = await externalFile(for: item) else {
                showToast("This session's markdown is not available yet")
                return
            }
            MarkdownOpener.revealInFinder(file)
        }
    }

    func copyMarkdown(_ item: SessionItem) {
        Task {
            var markdown = markdownCache[item.clientID]?.markdown
            if markdown == nil, selectedSession?.clientID == item.clientID {
                markdown = sessionMarkdown
            }
            if markdown == nil {
                await loadMarkdownIntoCache(for: item)
                markdown = markdownCache[item.clientID]?.markdown
            }
            guard let markdown, !markdown.isEmpty else {
                showToast("This session's markdown is not available yet")
                return
            }
            MarkdownOpener.copyToPasteboard(markdown)
            showToast("Session markdown copied")
        }
    }
}
