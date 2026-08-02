import AppKit
import SwiftUI

/// The Storage tab: where session markdown is written, globally and per
/// project, plus keeping history folders out of git.
struct StorageSettingsView: View {
    @ObservedObject var storage: StorageModel

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text("The folder where session markdown is written. By default each project keeps its own .specstory/history; a custom folder receives the files directly, nothing is appended.")
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
                .fixedSize(horizontal: false, vertical: true)

            globalCard
            projectsCard
        }
    }

    // MARK: Global

    private var globalCard: some View {
        VStack(alignment: .leading, spacing: 12) {
            sectionTitle("Global")

            HStack(alignment: .center, spacing: 10) {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Output folder")
                        .font(Theme.body(13, weight: .medium))
                        .foregroundStyle(Theme.ink)
                    if let dir = storage.globalOutputDir {
                        Text(dir)
                            .font(Theme.mono(11))
                            .foregroundStyle(Theme.inkSecondary)
                            .lineLimit(1)
                            .truncationMode(.middle)
                    } else {
                        Text("Per-project default")
                            .font(Theme.body(11))
                            .foregroundStyle(Theme.inkSecondary)
                    }
                }
                Spacer(minLength: 8)
                if storage.globalOutputDir != nil {
                    Button("Use per-project default") {
                        storage.setGlobalOutputDir(nil)
                    }
                    .controlSize(.small)
                }
                Button("Choose Folder") {
                    if let dir = pickFolder() {
                        storage.setGlobalOutputDir(dir)
                    }
                }
                .controlSize(.small)
            }

            Divider().overlay(Theme.hairline)

            Toggle(isOn: globalIgnoreBinding) {
                toggleLabel(
                    "Keep SpecStory history out of git everywhere (global excludes)",
                    detail: "Adds .specstory/history/ to your global git excludes file so no repository commits session markdown."
                )
            }
            .toggleStyle(.switch)
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardChrome()
    }

    private var globalIgnoreBinding: Binding<Bool> {
        Binding(
            get: { storage.globalGitIgnored },
            set: { storage.setGlobalGitIgnored($0) }
        )
    }

    // MARK: Projects

    private var projectsCard: some View {
        VStack(alignment: .leading, spacing: 12) {
            sectionTitle("Projects")
            if storage.projects.isEmpty {
                Text("Projects appear here once SpecStory has seen sessions in them.")
                    .font(Theme.body(12))
                    .foregroundStyle(Theme.inkSecondary)
            } else {
                VStack(spacing: 0) {
                    ForEach(storage.projects) { row in
                        projectRow(row)
                        if row.id != storage.projects.last?.id {
                            Divider().overlay(Theme.hairline)
                        }
                    }
                }
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardChrome()
    }

    private func projectRow(_ row: StorageModel.ProjectStorageRow) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(row.name)
                    .font(Theme.body(13, weight: .bold))
                    .foregroundStyle(Theme.ink)
                if row.overrideDir != nil {
                    Text("override")
                        .font(Theme.body(9, weight: .semibold))
                        .kerning(0.3)
                        .foregroundStyle(Theme.accent)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(Theme.accent.opacity(0.12), in: Capsule())
                }
                Spacer(minLength: 8)
                projectMenu(row)
            }
            Text(row.path)
                .font(Theme.body(11))
                .foregroundStyle(Theme.inkSecondary)
                .lineLimit(1)
                .truncationMode(.middle)
            Text(row.effectiveDir)
                .font(Theme.mono(11))
                .foregroundStyle(Theme.inkTertiary)
                .lineLimit(1)
                .truncationMode(.middle)
            Toggle(isOn: projectIgnoreBinding(row)) {
                Text("Ignore history in this repo's .gitignore")
                    .font(Theme.body(11))
                    .foregroundStyle(Theme.inkSecondary)
            }
            .toggleStyle(.switch)
            .controlSize(.mini)
            .padding(.top, 2)
        }
        .padding(.vertical, 10)
    }

    private func projectMenu(_ row: StorageModel.ProjectStorageRow) -> some View {
        Menu {
            Button("Choose custom folder...") {
                if let dir = pickFolder() {
                    storage.setProjectOutputDir(path: row.path, dir: dir)
                }
            }
            Button("Use global setting") {
                storage.setProjectOutputDir(path: row.path, dir: nil)
            }
            .disabled(row.overrideDir == nil)
            Divider()
            Button("Reveal history in Finder") {
                revealHistory(row)
            }
        } label: {
            Image(systemName: "ellipsis.circle")
                .font(.system(size: 13, weight: .medium))
                .foregroundStyle(Theme.inkSecondary)
                .frame(width: 24, height: 24)
                .background(Theme.card, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 6, style: .continuous).strokeBorder(Theme.hairline))
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .buttonStyle(.tactile)
        .fixedSize()
        .help("Storage options for this project")
    }

    private func projectIgnoreBinding(_ row: StorageModel.ProjectStorageRow) -> Binding<Bool> {
        Binding(
            get: { storage.projects.first(where: { $0.id == row.id })?.historyIgnored ?? row.historyIgnored },
            set: { storage.setHistoryIgnored(path: row.path, $0) }
        )
    }

    // MARK: Helpers

    private func revealHistory(_ row: StorageModel.ProjectStorageRow) {
        let target = FileManager.default.fileExists(atPath: row.effectiveDir) ? row.effectiveDir : row.path
        NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: target)])
    }

    private func pickFolder() -> String? {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = "Use this folder"
        guard panel.runModal() == .OK, let url = panel.url else { return nil }
        return url.path
    }

    private func toggleLabel(_ title: String, detail: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title)
                .font(Theme.body(13, weight: .medium))
                .foregroundStyle(Theme.ink)
            Text(detail)
                .font(Theme.body(11))
                .foregroundStyle(Theme.inkSecondary)
        }
        .padding(.trailing, 8)
    }

    private func sectionTitle(_ title: String) -> some View {
        Text(title)
            .font(Theme.body(11, weight: .semibold))
            .foregroundStyle(Theme.inkTertiary)
            .textCase(.uppercase)
            .kerning(0.4)
    }
}
