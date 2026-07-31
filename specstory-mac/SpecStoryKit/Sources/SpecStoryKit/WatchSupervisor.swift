import Foundation

/// Fleet of `specstory watch --json --silent` children, one per project path,
/// capped at maxChildren with LRU eviction by last event time.
///
/// All callbacks are delivered on an internal serial queue (never the main
/// actor). Do not call watchedProjects synchronously from inside a callback;
/// mutating methods are safe anywhere.
public final class WatchSupervisor {
    private final class Child {
        let runner: CLIRunner
        var lastEventAt = Date()
        var stopping = false
        /// When set, respawn after exit iff this still equals the supervisor
        /// generation (latest restartAll wins).
        var respawnGeneration: Int?

        init(runner: CLIRunner) {
            self.runner = runner
        }
    }

    private let binary: URL
    private let maxChildren: Int
    private let environmentProvider: () -> [String: String]
    private let queue = DispatchQueue(label: "com.specstory.mac.watch-supervisor")

    private var children: [String: Child] = [:]
    /// Paths currently considered watched, in insertion order. A child being
    /// drained after eviction is no longer listed here.
    private var order: [String] = []
    private var desired: Set<String> = []
    private var generation = 0
    private var lastUnexpectedExit: [String: Date] = [:]

    // Internal so tests can shrink the timing policy.
    var restartDelay: TimeInterval = 3
    var errorWindow: TimeInterval = 60
    var evictionPatience: TimeInterval = 20

    public var onEvent: ((String, WatchEvent) -> Void)?
    public var onLog: ((String, String, String) -> Void)?

    public init(binary: URL, maxChildren: Int, environmentProvider: @escaping () -> [String: String]) {
        self.binary = binary
        self.maxChildren = max(1, maxChildren)
        self.environmentProvider = environmentProvider
    }

    public var watchedProjects: [String] {
        queue.sync { order }
    }

    /// Reconcile the fleet to this set: keep existing, start missing, evict
    /// extras. Order expresses priority, so when the list exceeds the cap the
    /// leading (most recently active) paths win.
    public func setProjects(_ paths: [String]) {
        queue.async { self.reconcile(paths) }
    }

    /// Tripwire spin-up: promote an already watched path, or start a child,
    /// evicting the least recently active one when at capacity.
    public func ensureWatching(_ path: String) {
        queue.async { self.ensure(path) }
    }

    /// Terminate and respawn every child (auth/provider/config changes). A
    /// restartAll during churn keeps only the newest generation.
    public func restartAll() {
        queue.async {
            self.generation += 1
            for (path, child) in self.children where self.desired.contains(path) {
                child.respawnGeneration = self.generation
                if !child.stopping {
                    child.stopping = true
                    child.runner.terminate(patience: self.evictionPatience)
                }
                if !self.order.contains(path) { self.order.append(path) }
            }
        }
    }

    public func stopAll(patience: TimeInterval) {
        queue.async {
            self.generation += 1
            self.desired.removeAll()
            self.order.removeAll()
            for (_, child) in self.children {
                child.respawnGeneration = nil
                if !child.stopping {
                    child.stopping = true
                    child.runner.terminate(patience: patience)
                }
            }
        }
    }

    // MARK: - Queue-confined implementation

    private func reconcile(_ paths: [String]) {
        var seen = Set<String>()
        var target: [String] = []
        for path in paths where !seen.contains(path) {
            seen.insert(path)
            target.append(path)
            if target.count == maxChildren { break }
        }
        desired = Set(target)
        for path in order where !desired.contains(path) {
            stopChild(at: path, patience: evictionPatience)
        }
        for path in target { startIfNeeded(path) }
    }

    private func ensure(_ path: String) {
        desired.insert(path)
        if let child = children[path] {
            if child.stopping {
                child.respawnGeneration = generation
                if !order.contains(path) { order.append(path) }
            } else {
                child.lastEventAt = Date()
            }
            return
        }
        if order.count >= maxChildren {
            evictLeastRecentlyActive()
        }
        spawn(path)
    }

    private func evictLeastRecentlyActive() {
        let candidates = order.filter { children[$0].map { !$0.stopping } ?? false }
        if let lru = candidates.min(by: { children[$0]!.lastEventAt < children[$1]!.lastEventAt }) {
            desired.remove(lru)
            stopChild(at: lru, patience: evictionPatience)
        } else if let first = order.first {
            // Everything in order is a pending respawn; cancel the oldest.
            desired.remove(first)
            children[first]?.respawnGeneration = nil
            order.removeAll { $0 == first }
        }
    }

    private func startIfNeeded(_ path: String) {
        if let child = children[path] {
            if child.stopping {
                child.respawnGeneration = generation
                if !order.contains(path) { order.append(path) }
            }
            return
        }
        spawn(path)
    }

    private func stopChild(at path: String, patience: TimeInterval) {
        order.removeAll { $0 == path }
        guard let child = children[path], !child.stopping else { return }
        child.stopping = true
        child.respawnGeneration = nil
        child.runner.terminate(patience: patience)
    }

    private func spawn(_ path: String) {
        let runner = CLIRunner(
            binary: binary,
            arguments: ["watch", "--json", "--silent"],
            workingDirectory: path,
            environment: environmentProvider())
        let child = Child(runner: runner)
        children[path] = child
        if !order.contains(path) { order.append(path) }
        do {
            try runner.launch()
        } catch {
            children[path] = nil
            order.removeAll { $0 == path }
            desired.remove(path)
            onLog?(path, "error", "Could not start specstory watch: \(error.localizedDescription)")
            return
        }
        Task { [weak self] in
            for await event in runner.events {
                guard let self else { break }
                self.queue.async { self.handle(event, path: path, runner: runner) }
            }
        }
    }

    private func handle(_ event: CLIRunnerEvent, path: String, runner: CLIRunner) {
        guard let child = children[path], child.runner === runner else { return }
        switch event {
        case .watchEvent(let watchEvent):
            child.lastEventAt = Date()
            onEvent?(path, watchEvent)
        case .log(let level, let message):
            onLog?(path, level, message)
        case .terminated(let exitCode):
            childExited(child, path: path, exitCode: exitCode)
        }
    }

    private func childExited(_ child: Child, path: String, exitCode: Int32) {
        children[path] = nil
        if child.stopping {
            if let respawnGeneration = child.respawnGeneration,
                respawnGeneration == generation,
                desired.contains(path) {
                spawn(path)
            } else {
                order.removeAll { $0 == path }
            }
            return
        }

        order.removeAll { $0 == path }
        let now = Date()
        if let previous = lastUnexpectedExit[path], now.timeIntervalSince(previous) < errorWindow {
            desired.remove(path)
            onLog?(path, "error", "specstory watch for this project exited twice within a minute (exit code \(exitCode)). Watching is paused for this project.")
            return
        }
        lastUnexpectedExit[path] = now
        onLog?(path, "warn", "specstory watch exited unexpectedly (exit code \(exitCode)). Restarting shortly.")
        let expectedGeneration = generation
        queue.asyncAfter(deadline: .now() + restartDelay) { [weak self] in
            guard let self else { return }
            guard self.generation == expectedGeneration,
                self.desired.contains(path),
                self.children[path] == nil
            else { return }
            self.spawn(path)
        }
    }
}
