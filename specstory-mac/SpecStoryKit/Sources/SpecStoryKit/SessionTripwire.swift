import CoreServices
import Foundation

/// The Claude Code / Factory Droid project-directory encoding: the
/// symlink-resolved cwd with every character outside [a-zA-Z0-9-] replaced by
/// "-" (so "/Users/g/my.repo" becomes "-Users-g-my-repo").
enum ClaudeStyleProjectDirectory {
    static func encode(_ path: String) -> String {
        var encoded = ""
        for scalar in path.unicodeScalars {
            switch scalar {
            case "a"..."z", "A"..."Z", "0"..."9", "-":
                encoded.unicodeScalars.append(scalar)
            default:
                encoded.append("-")
            }
        }
        return encoded
    }

    /// The encoding is lossy ("-" can be a path separator or any replaced
    /// character), so decoding walks the real filesystem: at each level the
    /// next component must be an existing directory whose encoded name is a
    /// prefix of what remains. Falls back to naive dash-to-slash when the
    /// path no longer exists.
    static func decode(_ directoryName: String) -> String? {
        guard directoryName.hasPrefix("-"), directoryName.count > 1 else { return nil }
        let remaining = String(directoryName.dropFirst())
        var budget = 512
        if let resolved = resolve(directory: "/", remaining: remaining, budget: &budget) {
            return resolved
        }
        return "/" + remaining.replacingOccurrences(of: "-", with: "/")
    }

    private static func resolve(directory: String, remaining: String, budget: inout Int) -> String? {
        guard budget > 0 else { return nil }
        budget -= 1
        guard let entries = try? FileManager.default.contentsOfDirectory(atPath: directory) else {
            return nil
        }
        // Longest encoded match first, so "my-repo" beats "my" + "repo".
        let matches = entries.compactMap { entry -> (name: String, encoded: String)? in
            let encoded = encode(entry)
            guard !encoded.isEmpty else { return nil }
            guard remaining == encoded || remaining.hasPrefix(encoded + "-") else { return nil }
            return (entry, encoded)
        }.sorted { $0.encoded.count > $1.encoded.count }
        for match in matches {
            let child = directory == "/" ? "/" + match.name : directory + "/" + match.name
            var isDirectory: ObjCBool = false
            guard FileManager.default.fileExists(atPath: child, isDirectory: &isDirectory),
                isDirectory.boolValue
            else { continue }
            if remaining == match.encoded { return child }
            let rest = String(remaining.dropFirst(match.encoded.count + 1))
            if let resolved = resolve(directory: child, remaining: rest, budget: &budget) {
                return resolved
            }
        }
        return nil
    }
}

/// FSEvents tripwire over the provider session-store roots. Detection only:
/// it reports that an agent wrote something, and never parses provider files.
/// Missing roots are skipped and re-checked on a slow timer so agents
/// installed later still get picked up.
public final class SessionTripwire {
    private let roots: [ProviderRoot]
    private let latency: CFTimeInterval
    private let debounceInterval: TimeInterval
    private let rescanInterval: TimeInterval
    private let queue = DispatchQueue(label: "com.specstory.mac.session-tripwire")

    private struct ActiveRoot {
        let provider: Provider
        /// Literal root path plus its realpath: FSEvents reports canonical
        /// paths (/private/var/...) even when the stream was registered with
        /// a symlinked spelling (/var/...), so both must match.
        let matchPaths: [String]
    }

    private var stream: FSEventStreamRef?
    private var fallbackSources: [DispatchSourceFileSystemObject] = []
    private var activeRoots: [ActiveRoot] = []
    private var activeRootPaths: [String] = []
    private var rescanTimer: DispatchSourceTimer?
    private var lastFired: [Provider: Date] = [:]
    private var started = false
    // The FSEvents context holds an unretained pointer to self, so the
    // tripwire must stay alive while watching. stop() breaks the cycle.
    private var retainWhileWatching: SessionTripwire?

    public var onActivity: ((Provider, _ projectHint: String?) -> Void)?

    public convenience init(roots: [ProviderRoot]) {
        self.init(roots: roots, latency: 2.0, debounce: 5.0, rescanInterval: 30.0)
    }

    init(roots: [ProviderRoot], latency: CFTimeInterval, debounce: TimeInterval, rescanInterval: TimeInterval) {
        self.roots = roots
        self.latency = latency
        self.debounceInterval = debounce
        self.rescanInterval = rescanInterval
    }

    public func start() {
        queue.async {
            guard !self.started else { return }
            self.started = true
            self.buildWatchers()
            self.scheduleRescan()
        }
    }

    public func stop() {
        queue.async {
            self.started = false
            self.tearDownWatchers()
            self.rescanTimer?.cancel()
            self.rescanTimer = nil
        }
    }

    // MARK: - Queue-confined implementation

    private func buildWatchers() {
        tearDownWatchers()
        let existing = roots.filter { root in
            var isDirectory: ObjCBool = false
            return FileManager.default.fileExists(atPath: root.path, isDirectory: &isDirectory)
                && isDirectory.boolValue
        }
        guard !existing.isEmpty else { return }
        activeRootPaths = existing.map { $0.path }
        activeRoots = existing.map { root in
            var matchPaths = [root.path]
            if let real = Self.posixRealPath(root.path), real != root.path {
                matchPaths.append(real)
            }
            return ActiveRoot(provider: root.provider, matchPaths: matchPaths)
        }
        if !startFSEventStream(paths: activeRootPaths) {
            startFallbackSources(for: existing)
        }
        retainWhileWatching = self
    }

    private func tearDownWatchers() {
        if let stream {
            FSEventStreamStop(stream)
            FSEventStreamInvalidate(stream)
            FSEventStreamRelease(stream)
            self.stream = nil
        }
        for source in fallbackSources { source.cancel() }
        fallbackSources.removeAll()
        activeRoots = []
        activeRootPaths = []
        retainWhileWatching = nil
    }

    /// POSIX realpath. URL.resolvingSymlinksInPath is unusable here because
    /// it strips the /private prefix that FSEvents reports.
    private static func posixRealPath(_ path: String) -> String? {
        var buffer = [CChar](repeating: 0, count: Int(PATH_MAX))
        guard realpath(path, &buffer) != nil else { return nil }
        return String(cString: buffer)
    }

    private func startFSEventStream(paths: [String]) -> Bool {
        var context = FSEventStreamContext()
        context.info = Unmanaged.passUnretained(self).toOpaque()
        let flags = UInt32(kFSEventStreamCreateFlagUseCFTypes)
            | UInt32(kFSEventStreamCreateFlagFileEvents)
            | UInt32(kFSEventStreamCreateFlagNoDefer)
        let sinceNow = FSEventStreamEventId(bitPattern: Int64(-1))  // kFSEventStreamEventIdSinceNow
        guard let stream = FSEventStreamCreate(
            kCFAllocatorDefault,
            sessionTripwireFSEventsCallback,
            &context,
            paths as CFArray,
            sinceNow,
            latency,
            FSEventStreamCreateFlags(flags))
        else { return false }
        FSEventStreamSetDispatchQueue(stream, queue)
        guard FSEventStreamStart(stream) else {
            FSEventStreamInvalidate(stream)
            FSEventStreamRelease(stream)
            return false
        }
        self.stream = stream
        return true
    }

    /// Per-directory DispatchSource fallback when FSEvents is unavailable.
    /// Directory-level granularity only, so no project hint.
    private func startFallbackSources(for roots: [ProviderRoot]) {
        for root in roots {
            let descriptor = open(root.path, O_EVTONLY)
            guard descriptor >= 0 else { continue }
            let source = DispatchSource.makeFileSystemObjectSource(
                fileDescriptor: descriptor, eventMask: .write, queue: queue)
            let provider = root.provider
            source.setEventHandler { [weak self] in
                self?.fireIfNotDebounced(provider: provider, hint: nil)
            }
            source.setCancelHandler { close(descriptor) }
            source.resume()
            fallbackSources.append(source)
        }
    }

    private func scheduleRescan() {
        let timer = DispatchSource.makeTimerSource(queue: queue)
        timer.schedule(deadline: .now() + rescanInterval, repeating: rescanInterval)
        timer.setEventHandler { [weak self] in
            guard let self, self.started else { return }
            let existingNow = self.roots
                .filter { FileManager.default.fileExists(atPath: $0.path) }
                .map { $0.path }
            if Set(existingNow) != Set(self.activeRootPaths) {
                self.buildWatchers()
            }
        }
        timer.resume()
        rescanTimer = timer
    }

    fileprivate func handleFSEvents(paths: [String], flags: [FSEventStreamEventFlags]) {
        for (index, path) in paths.enumerated() where index < flags.count {
            let flag = flags[index]
            // Renames count as creation: atomic writes land files via rename.
            let created = flag & FSEventStreamEventFlags(kFSEventStreamEventFlagItemCreated) != 0
                || flag & FSEventStreamEventFlags(kFSEventStreamEventFlagItemRenamed) != 0
            let isFile = flag & FSEventStreamEventFlags(kFSEventStreamEventFlagItemIsFile) != 0
            guard created, isFile else { continue }
            guard let match = matchingRoot(for: path) else { continue }
            fireIfNotDebounced(
                provider: match.provider,
                hint: projectHint(provider: match.provider, rootPrefix: match.prefix, eventPath: path))
        }
    }

    private func matchingRoot(for eventPath: String) -> (provider: Provider, prefix: String)? {
        var best: (provider: Provider, prefix: String)?
        for active in activeRoots {
            for candidate in active.matchPaths
            where eventPath == candidate || eventPath.hasPrefix(candidate + "/") {
                if best == nil || candidate.count > best!.prefix.count {
                    best = (active.provider, candidate)
                }
            }
        }
        return best
    }

    private func projectHint(provider: Provider, rootPrefix: String, eventPath: String) -> String? {
        guard provider == .claude || provider == .droid else { return nil }
        let prefix = rootPrefix + "/"
        guard eventPath.hasPrefix(prefix) else { return nil }
        let relative = eventPath.dropFirst(prefix.count)
        guard let encodedDirectory = relative.split(separator: "/").first else { return nil }
        return ClaudeStyleProjectDirectory.decode(String(encodedDirectory))
    }

    private func fireIfNotDebounced(provider: Provider, hint: String?) {
        let now = Date()
        if let last = lastFired[provider], now.timeIntervalSince(last) < debounceInterval {
            return
        }
        lastFired[provider] = now
        onActivity?(provider, hint)
    }
}

private func sessionTripwireFSEventsCallback(
    _ streamRef: ConstFSEventStreamRef,
    _ clientCallBackInfo: UnsafeMutableRawPointer?,
    _ numEvents: Int,
    _ eventPaths: UnsafeMutableRawPointer,
    _ eventFlags: UnsafePointer<FSEventStreamEventFlags>,
    _ eventIds: UnsafePointer<FSEventStreamEventId>
) {
    guard let info = clientCallBackInfo else { return }
    let tripwire = Unmanaged<SessionTripwire>.fromOpaque(info).takeUnretainedValue()
    guard let paths = unsafeBitCast(eventPaths, to: CFArray.self) as? [String] else { return }
    let flags = Array(UnsafeBufferPointer(start: eventFlags, count: numEvents))
    tripwire.handleFSEvents(paths: paths, flags: flags)
}
