import Foundation

public struct GitDirtyFile: Sendable, Equatable, Identifiable {
    public let path: String
    public let status: String
    public var id: String { path }

    public init(path: String, status: String) {
        self.path = path
        self.status = status
    }
}

public struct GitWorkingState: Sendable, Equatable {
    public let branch: String?
    public let isWorktree: Bool
    public let dirtyFiles: [GitDirtyFile]
    public let insertions: Int?
    public let deletions: Int?

    public init(
        branch: String? = nil,
        isWorktree: Bool = false,
        dirtyFiles: [GitDirtyFile] = [],
        insertions: Int? = nil,
        deletions: Int? = nil
    ) {
        self.branch = branch
        self.isWorktree = isWorktree
        self.dirtyFiles = dirtyFiles
        self.insertions = insertions
        self.deletions = deletions
    }
}

/// Read-only git introspection for the live-session cockpit. Every command
/// runs /usr/bin/git with -C <path>, a 5 second timeout, and never throws;
/// a missing repo or a hung git yields nil.
public enum GitInspector {
    static let dirtyFilesCap = 40
    static let diffLineCap = 400
    static let commandTimeout: TimeInterval = 5

    public static func state(at path: String) -> GitWorkingState? {
        // status --porcelain doubles as the "is this a repo" gate: it works
        // even in a repo with no commits yet, where rev-parse HEAD fails.
        guard let status = runGit(["status", "--porcelain"], at: path), status.exitCode == 0 else {
            return nil
        }

        var branch: String?
        if let result = runGit(["rev-parse", "--abbrev-ref", "HEAD"], at: path), result.exitCode == 0 {
            let trimmed = result.output.trimmingCharacters(in: .whitespacesAndNewlines)
            if !trimmed.isEmpty { branch = trimmed }
        }

        var dirtyFiles: [GitDirtyFile] = []
        for line in status.output.components(separatedBy: "\n") {
            guard line.count > 3 else { continue }
            let statusCode = String(line.prefix(2)).trimmingCharacters(in: .whitespaces)
            let filePath = String(line.dropFirst(3))
            guard !filePath.isEmpty else { continue }
            dirtyFiles.append(GitDirtyFile(path: filePath, status: statusCode))
            if dirtyFiles.count == dirtyFilesCap { break }
        }

        var insertions: Int?
        var deletions: Int?
        if let shortstat = runGit(["diff", "--shortstat", "HEAD"], at: path), shortstat.exitCode == 0 {
            let output = shortstat.output.trimmingCharacters(in: .whitespacesAndNewlines)
            if !output.isEmpty {
                insertions = firstInt(before: "insertion", in: output) ?? 0
                deletions = firstInt(before: "deletion", in: output) ?? 0
            }
        }

        // A linked worktree marks its root with a .git FILE (a pointer to the
        // real gitdir); the main checkout has a .git directory.
        var isDirectory: ObjCBool = false
        let gitPath = (path as NSString).appendingPathComponent(".git")
        let gitEntryExists = FileManager.default.fileExists(atPath: gitPath, isDirectory: &isDirectory)
        let isWorktree = gitEntryExists && !isDirectory.boolValue

        return GitWorkingState(
            branch: branch,
            isWorktree: isWorktree,
            dirtyFiles: dirtyFiles,
            insertions: insertions,
            deletions: deletions
        )
    }

    public static func diff(at path: String, file: String?) -> String? {
        var arguments = ["diff", "HEAD"]
        if let file {
            arguments.append("--")
            arguments.append(file)
        }
        guard let result = runGit(arguments, at: path), result.exitCode == 0 else { return nil }
        var lines = result.output.components(separatedBy: "\n")
        if let last = lines.last, last.isEmpty { lines.removeLast() }
        if lines.count > diffLineCap {
            lines = Array(lines.prefix(diffLineCap))
            lines.append("... diff truncated ...")
        }
        return lines.joined(separator: "\n")
    }

    // MARK: - Plumbing

    private static func firstInt(before marker: String, in text: String) -> Int? {
        guard let markerRange = text.range(of: marker) else { return nil }
        let head = text[..<markerRange.lowerBound]
        let words = head.split(separator: " ")
        guard let last = words.last else { return nil }
        return Int(last)
    }

    private final class DataBox: @unchecked Sendable {
        var data = Data()
    }

    static func runGit(_ arguments: [String], at path: String) -> (output: String, exitCode: Int32)? {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/git")
        process.arguments = ["-C", path] + arguments
        process.environment = ProcessInfo.processInfo.environment.merging(
            ["GIT_TERMINAL_PROMPT": "0"], uniquingKeysWith: { _, new in new }
        )

        let stdoutPipe = Pipe()
        let stderrPipe = Pipe()
        process.standardOutput = stdoutPipe
        process.standardError = stderrPipe
        process.standardInput = FileHandle.nullDevice

        let terminated = DispatchSemaphore(value: 0)
        process.terminationHandler = { _ in terminated.signal() }

        do {
            try process.run()
        } catch {
            return nil
        }

        // Drain both pipes off-thread so a chatty git can never fill the 64 KB
        // pipe buffer and deadlock against our wait.
        let stdoutBox = DataBox()
        let stdoutDrained = DispatchSemaphore(value: 0)
        DispatchQueue.global(qos: .utility).async {
            stdoutBox.data = stdoutPipe.fileHandleForReading.readDataToEndOfFile()
            stdoutDrained.signal()
        }
        DispatchQueue.global(qos: .utility).async {
            _ = stderrPipe.fileHandleForReading.readDataToEndOfFile()
        }

        if terminated.wait(timeout: .now() + commandTimeout) == .timedOut {
            process.terminate()
            if terminated.wait(timeout: .now() + 0.5) == .timedOut {
                kill(process.processIdentifier, SIGKILL)
                terminated.wait()
            }
            return nil
        }

        stdoutDrained.wait()
        return (String(decoding: stdoutBox.data, as: UTF8.self), process.terminationStatus)
    }
}
