import SwiftUI
import SpecStoryKit

/// Menu content for opening a session's markdown outside the app: installed
/// editors with their real icons, Finder, and copy. Used by both the card
/// context menu and the detail header.
struct OpenInMenuContent: View {
    @ObservedObject var model: AppModel
    let item: SessionItem

    var body: some View {
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
