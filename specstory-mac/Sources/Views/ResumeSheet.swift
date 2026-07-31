import SwiftUI
import SpecStoryKit

/// Resume flow: pick the target agent, preview the exact command, launch in
/// the terminal or copy it. Mirrors the web app's resume dialog with a native
/// one-click path.
struct ResumeSheet: View {
    @ObservedObject var model: AppModel
    let item: SessionItem

    @State private var agent: Provider = .claude

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            VStack(alignment: .leading, spacing: 4) {
                Text("Resume session")
                    .font(Theme.display(20))
                    .foregroundStyle(Theme.ink)
                Text(item.title)
                    .font(Theme.body(12))
                    .foregroundStyle(Theme.inkSecondary)
                    .lineLimit(1)
            }

            Picker("Continue in", selection: $agent) {
                ForEach(AppModel.resumeTargets) { target in
                    Text(target.displayName).tag(target)
                }
            }
            .pickerStyle(.menu)

            VStack(alignment: .leading, spacing: 6) {
                Text("This command runs in your terminal:")
                    .font(Theme.body(11))
                    .foregroundStyle(Theme.inkSecondary)
                Text(model.resumeCommand(for: item, agent: agent))
                    .font(Theme.mono(11))
                    .foregroundStyle(Theme.ink)
                    .textSelection(.enabled)
                    .padding(10)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Theme.sidebarSelection, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            }

            if item.origin == .cloudOnly && !model.cliLoggedIn {
                Label {
                    Text("Resuming a cloud session from the terminal needs the CLI signed in once: run specstory login first.")
                } icon: {
                    Image(systemName: "info.circle")
                }
                .font(Theme.body(11))
                .foregroundStyle(Theme.inkSecondary)
            }

            HStack {
                Button("Copy command") {
                    TerminalLauncher.copyToPasteboard(command: model.resumeCommand(for: item, agent: agent))
                    model.resumeSheetItem = nil
                    model.showToast("Command copied to clipboard")
                }
                Spacer()
                Button("Cancel") {
                    model.resumeSheetItem = nil
                }
                .keyboardShortcut(.escape, modifiers: [])
                Button("Open in Terminal") {
                    model.performResume(item, agent: agent)
                }
                .buttonStyle(.borderedProminent)
                .tint(Theme.accent)
                .keyboardShortcut(.return, modifiers: [])
            }
        }
        .padding(22)
        .frame(width: 480)
        .onAppear {
            agent = model.resumeTarget(for: item)
        }
    }
}

/// Cloud-only sessions with no known local folder ask for one, remembered
/// per project.
struct FolderPickSheet: View {
    @ObservedObject var model: AppModel
    let item: SessionItem

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Where does this project live?")
                .font(Theme.display(20))
                .foregroundStyle(Theme.ink)
            Text("\(item.projectName ?? "This project") was synced from another machine. Choose its folder on this Mac so the session can resume there.")
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
                .fixedSize(horizontal: false, vertical: true)
            HStack {
                Spacer()
                Button("Cancel") {
                    model.folderPickItem = nil
                }
                .keyboardShortcut(.escape, modifiers: [])
                Button("Choose folder") {
                    chooseFolder()
                }
                .buttonStyle(.borderedProminent)
                .tint(Theme.accent)
            }
        }
        .padding(22)
        .frame(width: 440)
    }

    private func chooseFolder() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.prompt = "Use this folder"
        if panel.runModal() == .OK, let url = panel.url {
            model.completeFolderPick(for: item, folder: url)
        }
    }
}
