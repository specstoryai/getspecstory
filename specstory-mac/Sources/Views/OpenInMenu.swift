import SwiftUI
import SpecStoryKit

/// Menu content for opening a session's markdown outside the app: installed
/// editors with their real icons, Finder, and copy. Used by both the card
/// context menu and the detail header.
struct OpenInMenuContent: View {
    @ObservedObject var model: AppModel
    let item: SessionItem

    var body: some View {
        if let projectID = item.projectID {
            Link(destination: AppModel.cloudBaseURL.appendingPathComponent("projects/\(projectID)/sessions/\(item.clientID)")) {
                Label("Open in SpecStory Cloud", systemImage: "safari")
            }
            Divider()
        }
        Section("Open markdown in") {
            ForEach(model.installedEditors) { editor in
                Button {
                    model.openMarkdown(item, in: editor)
                } label: {
                    Label {
                        Text(editor.name)
                    } icon: {
                        Image(nsImage: editor.icon)
                    }
                }
            }
        }
        Divider()
        Button {
            model.revealMarkdownInFinder(item)
        } label: {
            Label("Reveal in Finder", systemImage: "folder")
        }
        Button {
            model.copyMarkdown(item)
        } label: {
            Label("Copy markdown", systemImage: "doc.on.doc")
        }
    }
}
