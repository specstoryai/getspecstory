import Foundation
import SpecStoryKit

/// Where session markdown lands on disk: the global output_dir, per-project
/// overrides, and git hygiene for history folders. Reads and writes go through
/// ConfigTOML; row building runs off-main. Output path changes fire onChanged
/// because they are in the watch restart matrix.
@MainActor
final class StorageModel: ObservableObject {
    struct ProjectStorageRow: Identifiable, Equatable {
        let path: String
        let name: String
        let overrideDir: String?
        let effectiveDir: String
        let historyIgnored: Bool
        var sessionCount: Int = 0
        var lastActivity: Date?
        var id: String { path }
    }

    /// Session metadata per project path, supplied by AppModel from the
    /// merged feed so rows can carry counts and recency.
    var metadataProvider: (() -> [String: (count: Int, last: Date?)])?

    @Published private(set) var globalOutputDir: String?
    @Published private(set) var projects: [ProjectStorageRow] = []
    @Published private(set) var globalGitIgnored = false

    /// Surfaced as a toast by AppModel.
    var onError: ((String) -> Void)?
    /// Fired after an output path change so AppModel can restart watchers.
    var onChanged: (() -> Void)?

    private var knownProjectPaths: [String] = []

    // MARK: Reads

    func refresh(projectPaths: [String]) async {
        var seen = Set<String>()
        let paths = projectPaths.filter { seen.insert($0).inserted }
        knownProjectPaths = paths
        let snapshot = await Task.detached(priority: .userInitiated) {
            StorageSnapshot.build(projectPaths: paths)
        }.value
        globalOutputDir = snapshot.globalOutputDir
        globalGitIgnored = snapshot.globalGitIgnored
        projects = snapshot.rows
        if let metadata = metadataProvider?() {
            projects = projects.map { row in
                var enriched = row
                if let meta = metadata[row.path] {
                    enriched.sessionCount = meta.count
                    enriched.lastActivity = meta.last
                }
                return enriched
            }
            // Busiest projects first; endless alphabetical scrolling is useless.
            projects.sort { ($0.lastActivity ?? .distantPast) > ($1.lastActivity ?? .distantPast) }
        }
    }

    // MARK: Output folders

    func setGlobalOutputDir(_ dir: String?) {
        do {
            try ConfigTOML.setOutputDir(dir, configURL: ConfigTOML.userConfigURL)
            onChanged?()
        } catch {
            onError?("Could not update the global output folder: \(error.localizedDescription)")
        }
        refreshKnown()
    }

    func setProjectOutputDir(path: String, dir: String?) {
        do {
            try ConfigTOML.setOutputDir(dir, configURL: ConfigTOML.projectConfigURL(projectPath: path))
            onChanged?()
        } catch {
            let name = SessionItem.folderName(of: path) ?? path
            onError?("Could not update the output folder for \(name): \(error.localizedDescription)")
        }
        refreshKnown()
    }

    // MARK: Git hygiene

    func setHistoryIgnored(path: String, _ ignored: Bool) {
        do {
            try HistoryIgnoreEntry.setPresent(ignored, inFileAt: path + "/.gitignore")
        } catch {
            let name = SessionItem.folderName(of: path) ?? path
            onError?("Could not edit .gitignore in \(name): \(error.localizedDescription)")
        }
        refreshKnown()
    }

    func setGlobalGitIgnored(_ ignored: Bool) {
        Task { [weak self] in
            // The git config lookup can block up to its 5 s timeout; keep it
            // off the main actor.
            let failure: String? = await Task.detached(priority: .userInitiated) {
                do {
                    try GlobalExcludes.setIgnored(ignored)
                    return nil
                } catch {
                    return "Could not update your global git excludes file: \(error.localizedDescription)"
                }
            }.value
            guard let self else { return }
            if let failure { self.onError?(failure) }
            await self.refresh(projectPaths: self.knownProjectPaths)
        }
    }

    private func refreshKnown() {
        Task { [weak self] in
            guard let self else { return }
            await self.refresh(projectPaths: self.knownProjectPaths)
        }
    }
}

// MARK: - Snapshot (built off-main)

private struct StorageSnapshot {
    var globalOutputDir: String?
    var globalGitIgnored: Bool
    var rows: [StorageModel.ProjectStorageRow]

    static func build(projectPaths: [String]) -> StorageSnapshot {
        let globalDir = ConfigTOML.outputDir(configURL: ConfigTOML.userConfigURL)
        let rows = projectPaths.map { path -> StorageModel.ProjectStorageRow in
            let override = ConfigTOML.outputDir(configURL: ConfigTOML.projectConfigURL(projectPath: path))
            return StorageModel.ProjectStorageRow(
                path: path,
                name: SessionItem.folderName(of: path) ?? (path as NSString).lastPathComponent,
                overrideDir: override,
                effectiveDir: override ?? globalDir ?? path + "/.specstory/history",
                historyIgnored: HistoryIgnoreEntry.isPresent(inFileAt: path + "/.gitignore")
            )
        }
        return StorageSnapshot(
            globalOutputDir: globalDir,
            globalGitIgnored: GlobalExcludes.isIgnored(),
            rows: rows
        )
    }
}

// MARK: - Gitignore entry editing

/// The one entry we manage in gitignore-style files: a line that is exactly
/// ".specstory/history/", preceded by our comment when we add it. Everything
/// else in the file is preserved verbatim.
enum HistoryIgnoreEntry {
    static let comment = "# SpecStory session history"
    static let entry = ".specstory/history/"

    static func isPresent(inFileAt path: String) -> Bool {
        guard let content = try? String(contentsOfFile: path, encoding: .utf8) else { return false }
        return content.components(separatedBy: "\n").contains(entry)
    }

    static func setPresent(_ present: Bool, inFileAt path: String) throws {
        let existing = (try? String(contentsOfFile: path, encoding: .utf8)) ?? ""
        var lines = existing.isEmpty ? [] : existing.components(separatedBy: "\n")
        // A trailing newline shows up as a final empty component; drop it so
        // appends land on real lines, and restore the newline on write.
        if lines.last == "" { lines.removeLast() }

        if present {
            guard !lines.contains(entry) else { return }
            lines.append(comment)
            lines.append(entry)
        } else {
            guard lines.contains(entry) || lines.contains(comment) else { return }
            lines.removeAll { $0 == entry || $0 == comment }
        }

        var output = lines.joined(separator: "\n")
        if !output.isEmpty { output += "\n" }
        try output.write(toFile: path, atomically: true, encoding: .utf8)
    }
}

// MARK: - Global git excludes

/// The user's global excludes file, resolved through git config. When unset
/// and the user enables the toggle, we point core.excludesFile at the modern
/// default (~/.config/git/ignore) so the entry actually takes effect.
enum GlobalExcludes {
    static var defaultPath: String { NSHomeDirectory() + "/.config/git/ignore" }

    static func isIgnored() -> Bool {
        HistoryIgnoreEntry.isPresent(inFileAt: resolvedPath() ?? defaultPath)
    }

    static func setIgnored(_ ignored: Bool) throws {
        let resolved = resolvedPath()
        if resolved == nil, ignored {
            // git only honors the entry once core.excludesFile is on record.
            _ = runGit(["config", "--global", "core.excludesFile", "~/.config/git/ignore"])
        }
        let path = resolved ?? defaultPath
        if ignored {
            try FileManager.default.createDirectory(
                atPath: (path as NSString).deletingLastPathComponent,
                withIntermediateDirectories: true
            )
        }
        try HistoryIgnoreEntry.setPresent(ignored, inFileAt: path)
    }

    static func resolvedPath() -> String? {
        guard let output = runGit(["config", "--global", "core.excludesFile"]) else { return nil }
        let trimmed = output.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        return (trimmed as NSString).expandingTildeInPath
    }

    private static func runGit(_ arguments: [String], timeout: TimeInterval = 5) -> String? {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/git")
        process.arguments = arguments
        let stdout = Pipe()
        process.standardOutput = stdout
        process.standardError = Pipe()
        do { try process.run() } catch { return nil }

        let deadline = Date().addingTimeInterval(timeout)
        while process.isRunning && Date() < deadline {
            usleep(50_000)
        }
        if process.isRunning {
            process.terminate()
            return nil
        }
        let data = stdout.fileHandleForReading.readDataToEndOfFile()
        guard process.terminationStatus == 0 else { return nil }
        return String(data: data, encoding: .utf8)
    }
}
