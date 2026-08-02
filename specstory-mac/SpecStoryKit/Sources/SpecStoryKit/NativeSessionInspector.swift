import Foundation

/// A best-effort snapshot of what a live session is doing right now, read
/// straight from the agent's own native session file (not SpecStory markdown).
///
/// Token counts are a tail-window approximation: they are summed over the
/// assistant lines visible in the last ~256 KB of the file, so on very long
/// sessions they undercount the session total. They are still monotonic
/// enough for a live cockpit readout.
public struct LiveSessionInsight: Sendable, Equatable {
    public let cwd: String?
    public let branch: String?
    public let model: String?
    public let touchedFiles: [String]
    public let lastActivity: String?
    public let waitingForPermission: Bool
    public let inputTokens: Int?
    public let outputTokens: Int?
    public let cacheReadTokens: Int?
    public let lastEventAt: Date?

    public init(
        cwd: String? = nil,
        branch: String? = nil,
        model: String? = nil,
        touchedFiles: [String] = [],
        lastActivity: String? = nil,
        waitingForPermission: Bool = false,
        inputTokens: Int? = nil,
        outputTokens: Int? = nil,
        cacheReadTokens: Int? = nil,
        lastEventAt: Date? = nil
    ) {
        self.cwd = cwd
        self.branch = branch
        self.model = model
        self.touchedFiles = touchedFiles
        self.lastActivity = lastActivity
        self.waitingForPermission = waitingForPermission
        self.inputTokens = inputTokens
        self.outputTokens = outputTokens
        self.cacheReadTokens = cacheReadTokens
        self.lastEventAt = lastEventAt
    }
}

/// Reads agent-native session files for the live-session cockpit.
///
/// Designed for multi-hundred-MB JSONL files: only the first 64 KB (header
/// facts like cwd and git branch, which Claude Code stamps on nearly every
/// line) and the last 256 KB (recent state) are ever read.
///
/// Never throws. Returns nil only when nothing at all is locatable.
public enum NativeSessionInspector {
    static let headerWindow = 64 * 1024
    static let tailWindow: UInt64 = 256 * 1024
    static let touchedFilesCap = 12
    static let codexFileScanCap = 200

    // MARK: - Public entry

    public static func inspect(provider: String, sessionID: String, projectPath: String?) -> LiveSessionInsight? {
        switch Provider(providerID: provider) {
        case .claude:
            return inspectClaude(
                sessionID: sessionID,
                projectPath: projectPath,
                projectsRoot: FileManager.default.homeDirectoryForCurrentUser
                    .appendingPathComponent(".claude/projects")
            )
        case .codex:
            return inspectCodex(
                sessionID: sessionID,
                projectPath: projectPath,
                sessionsRoot: FileManager.default.homeDirectoryForCurrentUser
                    .appendingPathComponent(".codex/sessions")
            )
        default:
            // Other providers have no native file we parse yet. Surface the
            // project path so the UI can still show git state.
            guard let projectPath else { return nil }
            return LiveSessionInsight(cwd: projectPath)
        }
    }

    // MARK: - Claude Code

    /// Claude Code encodes a project cwd into a directory name by replacing
    /// every "/" and "." with "-". Verified against real entries on disk:
    /// `/Users/gdc/getspecstory` -> `-Users-gdc-getspecstory` and
    /// `/private/tmp/claude-501/.tmp13dSMz/plain-workspace` ->
    /// `-private-tmp-claude-501--tmp13dSMz-plain-workspace`
    /// (the dot in `.tmp13dSMz` becomes the second dash of `--tmp`).
    static func encodedProjectPath(_ path: String) -> String {
        var normalized = path
        while normalized.count > 1, normalized.hasSuffix("/") {
            normalized.removeLast()
        }
        return String(normalized.map { $0 == "/" || $0 == "." ? "-" : $0 })
    }

    static func inspectClaude(sessionID: String, projectPath: String?, projectsRoot: URL) -> LiveSessionInsight? {
        var fileURL: URL?
        if let projectPath {
            let candidate = projectsRoot
                .appendingPathComponent(encodedProjectPath(projectPath))
                .appendingPathComponent(sessionID + ".jsonl")
            if FileManager.default.fileExists(atPath: candidate.path) {
                fileURL = candidate
            }
        }
        if fileURL == nil {
            // No project path (or a stale one): shallow-scan project dirs for
            // the session id. Existence checks only, one per directory.
            let dirs = (try? FileManager.default.contentsOfDirectory(atPath: projectsRoot.path)) ?? []
            for dir in dirs {
                let candidate = projectsRoot
                    .appendingPathComponent(dir)
                    .appendingPathComponent(sessionID + ".jsonl")
                if FileManager.default.fileExists(atPath: candidate.path) {
                    fileURL = candidate
                    break
                }
            }
        }
        guard let fileURL else {
            guard let projectPath else { return nil }
            return LiveSessionInsight(cwd: projectPath)
        }
        return insight(fromClaudeFile: fileURL, projectPath: projectPath)
    }

    static func insight(fromClaudeFile url: URL, projectPath: String?) -> LiveSessionInsight {
        let (headerLines, tailLines) = readWindows(url: url)

        // cwd and gitBranch ride on nearly every line; the header window is
        // the cheap place to find them, the tail is the fallback.
        var cwd: String?
        var branch: String?
        for line in headerLines + tailLines {
            if cwd == nil { cwd = line["cwd"] as? String }
            if branch == nil { branch = line["gitBranch"] as? String }
            if cwd != nil && branch != nil { break }
        }
        if cwd == nil { cwd = projectPath }

        var model: String?
        var lastActivity: String?
        var lastEventAt: Date?
        var inputTokens = 0
        var outputTokens = 0
        var cacheReadTokens = 0
        var sawUsage = false
        var touched: [String] = []

        for line in tailLines.reversed() {
            if model == nil,
               let message = line["message"] as? [String: Any],
               let lineModel = message["model"] as? String {
                model = lineModel
            }
            if lastEventAt == nil, let stamp = line["timestamp"] as? String {
                lastEventAt = parseISODate(stamp)
            }
            if lastActivity == nil, isSidechain(line) == false {
                lastActivity = activityLine(for: line)
            }
            if touched.count < touchedFilesCap, claudeLineType(line) == "assistant" {
                for block in contentBlocks(line).reversed() {
                    guard let path = editedFilePath(block) else { continue }
                    if !touched.contains(path) {
                        touched.append(path)
                        if touched.count == touchedFilesCap { break }
                    }
                }
            }
        }

        for line in tailLines where claudeLineType(line) == "assistant" {
            guard let message = line["message"] as? [String: Any],
                  let usage = message["usage"] as? [String: Any] else { continue }
            sawUsage = true
            inputTokens += intValue(usage["input_tokens"]) ?? 0
            outputTokens += intValue(usage["output_tokens"]) ?? 0
            cacheReadTokens += intValue(usage["cache_read_input_tokens"]) ?? 0
        }

        let displayFiles = touched.map { stripPrefix($0, cwd: cwd) }

        return LiveSessionInsight(
            cwd: cwd,
            branch: branch,
            model: model,
            touchedFiles: displayFiles,
            lastActivity: lastActivity,
            waitingForPermission: waitingForPermission(in: tailLines),
            inputTokens: sawUsage ? inputTokens : nil,
            outputTokens: sawUsage ? outputTokens : nil,
            cacheReadTokens: sawUsage ? cacheReadTokens : nil,
            lastEventAt: lastEventAt
        )
    }

    /// Best effort within the tail window: the newest non-sidechain assistant
    /// message carrying tool_use blocks is "waiting" when none of those ids
    /// has a subsequent tool_result and the user has not typed a new prompt
    /// since. An unmatched tool_use at EOF therefore reads as true.
    private static func waitingForPermission(in tailLines: [[String: Any]]) -> Bool {
        var newestAssistantIndex: Int?
        for (index, line) in tailLines.enumerated().reversed() {
            guard isSidechain(line) == false else { continue }
            if claudeLineType(line) == "assistant" {
                newestAssistantIndex = index
                break
            }
        }
        guard let newestAssistantIndex else { return false }
        let toolUseIDs = contentBlocks(tailLines[newestAssistantIndex]).compactMap { block -> String? in
            guard block["type"] as? String == "tool_use" else { return nil }
            return block["id"] as? String
        }
        guard !toolUseIDs.isEmpty else { return false }

        var resolvedIDs = Set<String>()
        for line in tailLines[(newestAssistantIndex + 1)...] {
            guard isSidechain(line) == false else { continue }
            guard claudeLineType(line) == "user" else { continue }
            var sawToolResult = false
            for block in contentBlocks(line) where block["type"] as? String == "tool_result" {
                sawToolResult = true
                if let id = block["tool_use_id"] as? String {
                    resolvedIDs.insert(id)
                }
            }
            if !sawToolResult {
                // A plain user prompt after the tool_use means the turn moved
                // on (for example, the permission was denied and the user
                // typed something new).
                return false
            }
        }
        return toolUseIDs.contains { !resolvedIDs.contains($0) }
    }

    private static func activityLine(for line: [String: Any]) -> String? {
        switch claudeLineType(line) {
        case "summary":
            return "Waiting at the prompt"
        case "assistant":
            let blocks = contentBlocks(line)
            if let tool = blocks.last(where: { $0["type"] as? String == "tool_use" }) {
                return runningLine(for: tool)
            }
            if blocks.isEmpty && (line["message"] as? [String: Any])?["content"] == nil {
                return nil
            }
            return "Writing a response"
        case "user":
            let blocks = contentBlocks(line)
            if blocks.contains(where: { $0["type"] as? String == "tool_result" }) {
                return "Processing tool results"
            }
            if let message = line["message"] as? [String: Any], message["content"] != nil {
                return "Waiting at the prompt"
            }
            return nil
        default:
            return nil
        }
    }

    private static func runningLine(for toolUse: [String: Any]) -> String {
        let name = toolUse["name"] as? String ?? "tool"
        let input = toolUse["input"] as? [String: Any] ?? [:]
        var detail: String?
        switch name {
        case "Bash":
            if let command = input["command"] as? String {
                let flattened = command
                    .replacingOccurrences(of: "\n", with: " ")
                    .trimmingCharacters(in: .whitespaces)
                detail = String(flattened.prefix(50))
            }
        case "Edit", "Write", "MultiEdit", "NotebookEdit":
            if let path = (input["file_path"] ?? input["notebook_path"]) as? String {
                detail = (path as NSString).lastPathComponent
            }
        case "Task":
            detail = input["description"] as? String
        default:
            detail = nil
        }
        if let detail, !detail.isEmpty {
            return "Running \(name): \(detail)"
        }
        return "Running \(name)"
    }

    private static func editedFilePath(_ block: [String: Any]) -> String? {
        guard block["type"] as? String == "tool_use",
              let name = block["name"] as? String,
              ["Edit", "Write", "MultiEdit", "NotebookEdit"].contains(name),
              let input = block["input"] as? [String: Any] else { return nil }
        return (input["file_path"] ?? input["notebook_path"]) as? String
    }

    private static func stripPrefix(_ path: String, cwd: String?) -> String {
        guard let cwd, path.hasPrefix(cwd + "/") else { return path }
        return String(path.dropFirst(cwd.count + 1))
    }

    private static func claudeLineType(_ line: [String: Any]) -> String? {
        line["type"] as? String
    }

    private static func isSidechain(_ line: [String: Any]) -> Bool {
        line["isSidechain"] as? Bool ?? false
    }

    private static func contentBlocks(_ line: [String: Any]) -> [[String: Any]] {
        guard let message = line["message"] as? [String: Any] else { return [] }
        return message["content"] as? [[String: Any]] ?? []
    }

    // MARK: - Codex

    static func inspectCodex(sessionID: String, projectPath: String?, sessionsRoot: URL) -> LiveSessionInsight? {
        guard let fileURL = locateCodexFile(sessionID: sessionID, sessionsRoot: sessionsRoot) else {
            guard let projectPath else { return nil }
            return LiveSessionInsight(cwd: projectPath)
        }
        return insight(fromCodexFile: fileURL, projectPath: projectPath)
    }

    /// Walks ~/.codex/sessions/YYYY/MM/DD recent-first looking for a rollout
    /// file that names the session id (filename match first, then the first
    /// KB of content), examining at most 200 files.
    static func locateCodexFile(sessionID: String, sessionsRoot: URL) -> URL? {
        let fm = FileManager.default
        var examined = 0
        for year in sortedNumericSubdirs(of: sessionsRoot) {
            for month in sortedNumericSubdirs(of: year) {
                for day in sortedNumericSubdirs(of: month) {
                    let files = ((try? fm.contentsOfDirectory(atPath: day.path)) ?? [])
                        .filter { $0.hasSuffix(".jsonl") }
                        .sorted(by: >)
                    for file in files {
                        examined += 1
                        if examined > codexFileScanCap { return nil }
                        let url = day.appendingPathComponent(file)
                        if file.contains(sessionID) { return url }
                        if let handle = try? FileHandle(forReadingFrom: url) {
                            let head = (try? handle.read(upToCount: 1024)) ?? Data()
                            try? handle.close()
                            if String(decoding: head, as: UTF8.self).contains(sessionID) {
                                return url
                            }
                        }
                    }
                }
            }
        }
        return nil
    }

    private static func sortedNumericSubdirs(of url: URL) -> [URL] {
        let names = (try? FileManager.default.contentsOfDirectory(atPath: url.path)) ?? []
        return names
            .filter { !$0.isEmpty && $0.allSatisfy(\.isNumber) }
            .sorted(by: >)
            .map { url.appendingPathComponent($0) }
    }

    static func insight(fromCodexFile url: URL, projectPath: String?) -> LiveSessionInsight {
        let (headerLines, tailLines) = readWindows(url: url)

        var cwd: String?
        var branch: String?
        var model: String?
        var lastActivity: String?
        var lastEventAt: Date?
        var inputTokens: Int?
        var outputTokens: Int?
        var cacheReadTokens: Int?

        for line in headerLines + tailLines {
            let payload = line["payload"] as? [String: Any] ?? [:]
            if cwd == nil { cwd = (payload["cwd"] ?? line["cwd"]) as? String }
            if branch == nil {
                let git = payload["git"] as? [String: Any] ?? [:]
                branch = (git["branch"] ?? payload["branch"]) as? String
            }
            if model == nil { model = (payload["model"] ?? line["model"]) as? String }
        }
        if cwd == nil { cwd = projectPath }

        for line in tailLines.reversed() {
            let payload = line["payload"] as? [String: Any] ?? [:]
            if lastEventAt == nil, let stamp = line["timestamp"] as? String {
                lastEventAt = parseISODate(stamp)
            }
            if lastActivity == nil {
                if let eventType = (payload["type"] ?? line["type"]) as? String {
                    lastActivity = humanizedCodexType(eventType)
                }
            }
            // Codex emits cumulative token_count events; the newest one wins.
            if inputTokens == nil,
               payload["type"] as? String == "token_count",
               let info = payload["info"] as? [String: Any],
               let totals = info["total_token_usage"] as? [String: Any] {
                inputTokens = intValue(totals["input_tokens"])
                outputTokens = intValue(totals["output_tokens"])
                cacheReadTokens = intValue(totals["cached_input_tokens"])
            }
            if lastActivity != nil && lastEventAt != nil && inputTokens != nil { break }
        }

        return LiveSessionInsight(
            cwd: cwd,
            branch: branch,
            model: model,
            touchedFiles: [],
            lastActivity: lastActivity,
            waitingForPermission: false,
            inputTokens: inputTokens,
            outputTokens: outputTokens,
            cacheReadTokens: cacheReadTokens,
            lastEventAt: lastEventAt
        )
    }

    private static func humanizedCodexType(_ type: String) -> String {
        let spaced = type.replacingOccurrences(of: "_", with: " ")
        guard let first = spaced.first else { return spaced }
        return String(first).uppercased() + spaced.dropFirst()
    }

    // MARK: - Windowed reads

    /// Reads the first 64 KB and the last 256 KB of a JSONL file, returning
    /// parsed complete lines from each window. When the file fits inside the
    /// tail window the whole file becomes the tail. Never throws.
    static func readWindows(url: URL) -> (header: [[String: Any]], tail: [[String: Any]]) {
        guard let handle = try? FileHandle(forReadingFrom: url) else { return ([], []) }
        defer { try? handle.close() }

        let headerData = (try? handle.read(upToCount: headerWindow)).flatMap { $0 } ?? Data()
        let size = (try? handle.seekToEnd()) ?? UInt64(headerData.count)

        var tailData: Data
        if size > tailWindow {
            try? handle.seek(toOffset: size - tailWindow)
            tailData = (try? handle.readToEnd()).flatMap { $0 } ?? Data()
            // The window almost never lands on a line boundary; drop the
            // first partial line.
            if let newline = tailData.firstIndex(of: 0x0A) {
                tailData = tailData.subdata(in: tailData.index(after: newline)..<tailData.endIndex)
            } else {
                tailData = Data()
            }
        } else {
            try? handle.seek(toOffset: 0)
            tailData = (try? handle.readToEnd()).flatMap { $0 } ?? Data()
        }

        let headerComplete = size > UInt64(headerData.count)
        return (
            parseLines(headerData, dropLastPartial: headerComplete),
            parseLines(tailData, dropLastPartial: false)
        )
    }

    private static func parseLines(_ data: Data, dropLastPartial: Bool) -> [[String: Any]] {
        guard !data.isEmpty else { return [] }
        var segments = String(decoding: data, as: UTF8.self).components(separatedBy: "\n")
        if dropLastPartial, !segments.isEmpty {
            segments.removeLast()
        }
        var lines: [[String: Any]] = []
        lines.reserveCapacity(segments.count)
        for segment in segments {
            let trimmed = segment.trimmingCharacters(in: .whitespaces)
            guard trimmed.hasPrefix("{"), let lineData = trimmed.data(using: .utf8) else { continue }
            guard let object = try? JSONSerialization.jsonObject(with: lineData) as? [String: Any] else { continue }
            lines.append(object)
        }
        return lines
    }

    private static func intValue(_ value: Any?) -> Int? {
        if let int = value as? Int { return int }
        if let number = value as? NSNumber { return number.intValue }
        return nil
    }

    private static let isoWithFractional: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()

    private static let isoPlain: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime]
        return formatter
    }()

    static func parseISODate(_ string: String) -> Date? {
        isoWithFractional.date(from: string) ?? isoPlain.date(from: string)
    }
}
