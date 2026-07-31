import SwiftUI
import SpecStoryKit

/// Full session view: header with provenance and actions, rendered markdown.
struct SessionDetailView: View {
    @ObservedObject var model: AppModel
    let item: SessionItem

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider().overlay(Theme.hairline)
            contentBody
        }
        .background(Theme.paper)
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 10) {
            Button {
                model.closeSession()
            } label: {
                Label("Back", systemImage: "chevron.left")
                    .font(Theme.body(12, weight: .medium))
                    .foregroundStyle(Theme.inkSecondary)
            }
            .buttonStyle(.plain)

            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 6) {
                    Text(item.title)
                        .font(Theme.display(24))
                        .foregroundStyle(Theme.ink)
                        .lineLimit(2)
                    HStack(spacing: 6) {
                        let provider = Provider(providerID: item.provider)
                        Text(provider?.displayName ?? item.provider.capitalized)
                            .foregroundStyle(provider?.badgeColor ?? Theme.inkSecondary)
                        if let project = item.projectName {
                            Text("·").foregroundStyle(Theme.inkTertiary)
                            Text(project)
                        }
                        if let machine = item.machineName, item.isFromOtherMachine(currentDeviceID: DeviceIdentity.current) {
                            Text("·").foregroundStyle(Theme.inkTertiary)
                            Label(machine, systemImage: "laptopcomputer")
                        }
                        if let date = item.updatedAt ?? item.createdAt {
                            Text("·").foregroundStyle(Theme.inkTertiary)
                            Text(date.formatted(date: .abbreviated, time: .shortened))
                        }
                    }
                    .font(Theme.body(12))
                    .foregroundStyle(Theme.inkSecondary)
                }
                Spacer()
                HStack(spacing: 8) {
                    if let projectID = item.projectID {
                        Link(destination: AppModel.cloudBaseURL.appendingPathComponent("projects/\(projectID)/sessions/\(item.clientID)")) {
                            Label("Open in Cloud", systemImage: "safari")
                                .font(Theme.body(12))
                        }
                        .buttonStyle(.bordered)
                    }
                    if model.canResume(item) {
                        Button {
                            model.requestResume(item)
                        } label: {
                            Label("Resume", systemImage: "arrow.uturn.forward")
                                .font(Theme.body(12, weight: .medium))
                        }
                        .buttonStyle(.borderedProminent)
                        .tint(Theme.accent)
                    }
                }
            }
        }
        .padding(.horizontal, 28)
        .padding(.top, 18)
        .padding(.bottom, 14)
    }

    @ViewBuilder private var contentBody: some View {
        if model.detailLoading {
            VStack {
                Spacer()
                ProgressView("Loading session")
                    .controlSize(.small)
                Spacer()
            }
            .frame(maxWidth: .infinity)
        } else if let markdown = model.sessionMarkdown {
            MarkdownScrollView(markdown: markdown)
        } else {
            VStack(spacing: 8) {
                Spacer()
                Image(systemName: "doc.text")
                    .font(.system(size: 28, weight: .light))
                    .foregroundStyle(Theme.inkTertiary)
                Text("This session's content is not available here yet")
                    .font(Theme.body(13, weight: .medium))
                    .foregroundStyle(Theme.ink)
                Text(unavailableReason)
                    .font(Theme.body(12))
                    .foregroundStyle(Theme.inkSecondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 380)
                Spacer()
            }
            .frame(maxWidth: .infinity)
        }
    }

    private var unavailableReason: String {
        if item.origin == .cloudOnly, model.signedInEmail == nil {
            return "Sign in to SpecStory Cloud to read sessions synced from your machines."
        }
        return "The session may still be syncing, or its project folder is not reachable on this Mac."
    }
}

/// Renders session markdown in lightweight blocks so long transcripts stay
/// responsive (one AttributedString for a whole session is too slow).
struct MarkdownScrollView: View {
    let markdown: String

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 10) {
                ForEach(Array(blocks.enumerated()), id: \.offset) { _, block in
                    MarkdownBlockView(block: block)
                }
            }
            .padding(.horizontal, 28)
            .padding(.vertical, 20)
            .frame(maxWidth: Theme.feedWidth, alignment: .leading)
            .frame(maxWidth: .infinity)
        }
    }

    private var blocks: [String] {
        markdown.components(separatedBy: "\n\n").filter { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    }
}

struct MarkdownBlockView: View {
    let block: String

    var body: some View {
        if block.hasPrefix("```") {
            Text(block.trimmingCharacters(in: CharacterSet(charactersIn: "`\n")))
                .font(Theme.mono(11))
                .foregroundStyle(Theme.ink)
                .textSelection(.enabled)
                .padding(10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Theme.sidebarSelection, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
        } else if block.hasPrefix("#") {
            Text(block.drop(while: { $0 == "#" || $0 == " " }))
                .font(Theme.display(17))
                .foregroundStyle(Theme.ink)
                .textSelection(.enabled)
                .padding(.top, 6)
        } else {
            Text(rendered)
                .font(Theme.body(12.5))
                .foregroundStyle(Theme.ink)
                .textSelection(.enabled)
                .lineSpacing(3)
        }
    }

    private var rendered: AttributedString {
        (try? AttributedString(
            markdown: block,
            options: AttributedString.MarkdownParsingOptions(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        )) ?? AttributedString(block)
    }
}
