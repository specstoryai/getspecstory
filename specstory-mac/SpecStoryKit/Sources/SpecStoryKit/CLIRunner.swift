import Foundation

public enum CLIRunnerEvent: Sendable {
    case watchEvent(WatchEvent)
    case log(level: String, message: String)
    case terminated(exitCode: Int32)
}

public enum CLIRunnerError: Error, LocalizedError {
    case alreadyLaunched
    case launchFailed(String)
    case timeout(TimeInterval)

    public var errorDescription: String? {
        switch self {
        case .alreadyLaunched:
            return "This CLI process was already launched."
        case .launchFailed(let reason):
            return "Could not launch the specstory CLI: \(reason)"
        case .timeout(let seconds):
            return "The specstory CLI did not finish within \(Int(seconds)) seconds and was killed."
        }
    }
}

/// One-shot result for callers that need the exit code (specstory check exits
/// 2 on validation failure, which is a signal rather than a launch error).
public struct CLIRunResult: Sendable {
    public let stdout: String
    public let stderr: String
    public let exitCode: Int32
}

/// Splits pipe chunks into complete lines, holding the trailing partial line
/// until more data (or EOF flush) arrives. Internal on purpose: the public
/// NDJSONLineBuffer type is owned by the chat-stream module.
struct CLILineBuffer {
    private var pending = Data()

    mutating func append(_ chunk: Data) -> [String] {
        pending.append(chunk)
        var lines: [String] = []
        while let newlineIndex = pending.firstIndex(of: 0x0A) {
            let lineData = pending.subdata(in: pending.startIndex..<newlineIndex)
            pending.removeSubrange(pending.startIndex...newlineIndex)
            var line = String(decoding: lineData, as: UTF8.self)
            if line.hasSuffix("\r") { line.removeLast() }
            lines.append(line)
        }
        return lines
    }

    mutating func flush() -> String? {
        guard !pending.isEmpty else { return nil }
        let line = String(decoding: pending, as: UTF8.self)
        pending.removeAll()
        return line
    }
}

/// One child `specstory` process. Always appends --no-version-check (a
/// vendored binary must never self-update-check). stdout is parsed line-wise:
/// JSON lines become watch events, slog lines are re-leveled, everything else
/// is info logging. stderr lines become log events too. The stream ends with
/// exactly one .terminated event.
public final class CLIRunner {
    private let process = Process()
    private let stdoutPipe = Pipe()
    private let stderrPipe = Pipe()
    private let queue = DispatchQueue(label: "com.specstory.mac.cli-runner")

    private var stdoutBuffer = CLILineBuffer()
    private var stderrBuffer = CLILineBuffer()
    private var continuation: AsyncStream<CLIRunnerEvent>.Continuation?
    private var launched = false
    private var stdoutClosed = false
    private var stderrClosed = false
    private var exitStatus: Int32?
    private var finished = false
    // Keeps the runner alive while the child runs even if the caller drops
    // its reference; released when the event stream finishes.
    private var keepAliveWhileRunning: CLIRunner?

    public let events: AsyncStream<CLIRunnerEvent>

    public init(binary: URL, arguments: [String], workingDirectory: String, environment: [String: String]) {
        var streamContinuation: AsyncStream<CLIRunnerEvent>.Continuation?
        events = AsyncStream { streamContinuation = $0 }
        continuation = streamContinuation

        process.executableURL = binary
        process.arguments = Self.withNoVersionCheck(arguments)
        process.currentDirectoryURL = URL(fileURLWithPath: workingDirectory)
        process.environment = Self.mergedEnvironment(environment)
        process.standardOutput = stdoutPipe
        process.standardError = stderrPipe
        process.standardInput = FileHandle.nullDevice
    }

    public var isRunning: Bool {
        launched && process.isRunning
    }

    public func launch() throws {
        guard !launched else { throw CLIRunnerError.alreadyLaunched }
        stdoutPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            self?.queue.async { self?.consume(data, stderr: false) }
        }
        stderrPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            self?.queue.async { self?.consume(data, stderr: true) }
        }
        process.terminationHandler = { [weak self] process in
            let status = process.terminationStatus
            self?.queue.async { self?.processExited(status) }
        }
        do {
            try process.run()
        } catch {
            stdoutPipe.fileHandleForReading.readabilityHandler = nil
            stderrPipe.fileHandleForReading.readabilityHandler = nil
            process.terminationHandler = nil
            throw CLIRunnerError.launchFailed(error.localizedDescription)
        }
        launched = true
        keepAliveWhileRunning = self
    }

    /// SIGTERM first so the CLI can flush cloud sync (up to 180 s); escalate
    /// to SIGKILL only after `patience`. Callers pass a longer patience when
    /// uploads may be pending.
    public func terminate(patience: TimeInterval = 20) {
        queue.async { [self] in
            guard launched, process.isRunning else { return }
            let pid = process.processIdentifier
            process.terminate()
            queue.asyncAfter(deadline: .now() + patience) { [weak self] in
                guard let self, self.process.isRunning else { return }
                kill(pid, SIGKILL)
            }
        }
    }

    // MARK: - One-shot

    /// Run to completion and return stdout with interleaved slog lines
    /// stripped (VSIX precedent: drop lines starting with "time="). Kills the
    /// child and throws on timeout.
    public static func run(binary: URL, arguments: [String], workingDirectory: String, environment: [String: String], timeout: TimeInterval) async throws -> String {
        try await runDetailed(binary: binary, arguments: arguments, workingDirectory: workingDirectory, environment: environment, timeout: timeout).stdout
    }

    public static func runDetailed(binary: URL, arguments: [String], workingDirectory: String, environment: [String: String], timeout: TimeInterval) async throws -> CLIRunResult {
        try await withCheckedThrowingContinuation { continuation in
            let process = Process()
            let stdoutPipe = Pipe()
            let stderrPipe = Pipe()
            process.executableURL = binary
            process.arguments = withNoVersionCheck(arguments)
            process.currentDirectoryURL = URL(fileURLWithPath: workingDirectory)
            process.environment = mergedEnvironment(environment)
            process.standardOutput = stdoutPipe
            process.standardError = stderrPipe
            process.standardInput = FileHandle.nullDevice

            let lock = NSLock()
            var resumed = false
            func resumeOnce(_ result: Result<CLIRunResult, Error>) {
                lock.lock()
                let shouldResume = !resumed
                resumed = true
                lock.unlock()
                if shouldResume { continuation.resume(with: result) }
            }

            var stdoutData = Data()
            var stderrData = Data()
            let group = DispatchGroup()
            group.enter()
            group.enter()
            group.enter()
            DispatchQueue.global().async {
                stdoutData = (try? stdoutPipe.fileHandleForReading.readToEnd()) ?? Data()
                group.leave()
            }
            DispatchQueue.global().async {
                stderrData = (try? stderrPipe.fileHandleForReading.readToEnd()) ?? Data()
                group.leave()
            }
            process.terminationHandler = { _ in group.leave() }

            do {
                try process.run()
            } catch {
                process.terminationHandler = nil
                // Unblock the reader tasks; the child never existed.
                try? stdoutPipe.fileHandleForWriting.close()
                try? stderrPipe.fileHandleForWriting.close()
                resumeOnce(.failure(CLIRunnerError.launchFailed(error.localizedDescription)))
                return
            }

            let pid = process.processIdentifier
            var timedOut = false
            let killOnTimeout = DispatchWorkItem {
                lock.lock()
                timedOut = true
                lock.unlock()
                if process.isRunning { kill(pid, SIGKILL) }
                // Report the timeout shortly even if an orphaned grandchild
                // still holds the pipes open past the kill.
                DispatchQueue.global().asyncAfter(deadline: .now() + 0.2) {
                    resumeOnce(.failure(CLIRunnerError.timeout(timeout)))
                }
            }
            DispatchQueue.global().asyncAfter(deadline: .now() + timeout, execute: killOnTimeout)

            group.notify(queue: DispatchQueue.global()) {
                killOnTimeout.cancel()
                lock.lock()
                let sawTimeout = timedOut
                lock.unlock()
                if sawTimeout {
                    resumeOnce(.failure(CLIRunnerError.timeout(timeout)))
                    return
                }
                let stdout = stripSlogLines(String(decoding: stdoutData, as: UTF8.self))
                let stderr = String(decoding: stderrData, as: UTF8.self)
                resumeOnce(.success(CLIRunResult(stdout: stdout, stderr: stderr, exitCode: process.terminationStatus)))
            }
        }
    }

    // MARK: - Line classification

    static func withNoVersionCheck(_ arguments: [String]) -> [String] {
        arguments.contains("--no-version-check") ? arguments : arguments + ["--no-version-check"]
    }

    static func mergedEnvironment(_ overrides: [String: String]) -> [String: String] {
        ProcessInfo.processInfo.environment.merging(overrides) { _, override in override }
    }

    static func stripSlogLines(_ output: String) -> String {
        output.split(separator: "\n", omittingEmptySubsequences: false)
            .filter { !$0.hasPrefix("time=") }
            .joined(separator: "\n")
    }

    static func stdoutEvent(for line: String) -> CLIRunnerEvent {
        if line.hasPrefix("{"), let event = WatchEvent.parse(line: line) {
            return .watchEvent(event)
        }
        if let slog = parseSlogLine(line) {
            return .log(level: slog.level, message: slog.message)
        }
        return .log(level: "info", message: line)
    }

    static func stderrEvent(for line: String) -> CLIRunnerEvent {
        if let slog = parseSlogLine(line) {
            return .log(level: slog.level, message: slog.message)
        }
        return .log(level: "error", message: line)
    }

    /// Go slog text lines: time=... level=LEVEL msg="..." key=val
    static func parseSlogLine(_ line: String) -> (level: String, message: String)? {
        guard line.hasPrefix("time=") else { return nil }
        guard let levelRange = line.range(of: " level=") else { return nil }
        let level = String(line[levelRange.upperBound...].prefix(while: { $0 != " " })).lowercased()
        guard !level.isEmpty else { return nil }
        guard let msgRange = line.range(of: " msg=") else { return nil }
        let rest = String(line[msgRange.upperBound...])
        guard rest.hasPrefix("\"") else {
            return (level, rest)
        }
        var message = ""
        var escaped = false
        var index = rest.index(after: rest.startIndex)
        while index < rest.endIndex {
            let character = rest[index]
            if escaped {
                message.append(character)
                escaped = false
            } else if character == "\\" {
                escaped = true
            } else if character == "\"" {
                break
            } else {
                message.append(character)
            }
            index = rest.index(after: index)
        }
        // Keep trailing structured attrs so no information is dropped.
        if index < rest.endIndex {
            let tail = rest[rest.index(after: index)...].trimmingCharacters(in: .whitespaces)
            if !tail.isEmpty { message += " " + tail }
        }
        return (level, message)
    }

    // MARK: - Private

    private func consume(_ data: Data, stderr: Bool) {
        if data.isEmpty {
            if stderr {
                stderrClosed = true
                stderrPipe.fileHandleForReading.readabilityHandler = nil
            } else {
                stdoutClosed = true
                stdoutPipe.fileHandleForReading.readabilityHandler = nil
            }
            finishIfComplete()
            return
        }
        let lines = stderr ? stderrBuffer.append(data) : stdoutBuffer.append(data)
        for line in lines { emit(line, stderr: stderr) }
    }

    private func emit(_ line: String, stderr: Bool) {
        guard !finished, !line.isEmpty else { return }
        continuation?.yield(stderr ? Self.stderrEvent(for: line) : Self.stdoutEvent(for: line))
    }

    private func processExited(_ status: Int32) {
        exitStatus = status
        // If an orphaned grandchild inherited our pipes, EOF may never come;
        // force-finish shortly after the child itself is gone.
        queue.asyncAfter(deadline: .now() + 1.0) { [weak self] in
            self?.forceFinish()
        }
        finishIfComplete()
    }

    private func finishIfComplete() {
        guard !finished, let status = exitStatus, stdoutClosed, stderrClosed else { return }
        finishStream(status)
    }

    private func forceFinish() {
        guard !finished, let status = exitStatus else { return }
        stdoutClosed = true
        stderrClosed = true
        stdoutPipe.fileHandleForReading.readabilityHandler = nil
        stderrPipe.fileHandleForReading.readabilityHandler = nil
        finishStream(status)
    }

    private func finishStream(_ status: Int32) {
        if let line = stdoutBuffer.flush() { emit(line, stderr: false) }
        if let line = stderrBuffer.flush() { emit(line, stderr: true) }
        finished = true
        continuation?.yield(.terminated(exitCode: status))
        continuation?.finish()
        continuation = nil
        keepAliveWhileRunning = nil
    }
}
