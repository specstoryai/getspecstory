import Foundation
import SpecStoryKit

extension AppModel {
    // MARK: Feed assembly (local + cloud merge)

    func refreshFeed() async {
        lastFeedRefreshAt = Date()
        // SQLite work stays off the main actor (LIKE fallbacks can scan).
        let reader = indexReader
        let locals = await Task.detached(priority: .userInitiated) {
            (try? reader?.recentSessions(limit: 500)) ?? [IndexedSession]()
        }.value

        var clouds = [CloudSession]()
        var projectNames = [String: String]()
        if case .signedIn = authState, cloudSyncEnabled {
            do {
                async let sessionsCall = auth.api.recentSessions(limit: 200)
                async let projectsCall = auth.api.projects()
                let (sessions, projects) = try await (sessionsCall, projectsCall)
                clouds = sessions
                cloudProjects = projects
                projectNames = Dictionary(projects.map { ($0.id, $0.name) }, uniquingKeysWith: { first, _ in first })
                cloudError = nil
            } catch {
                cloudError = friendlyCloudError(error)
            }
        }

        allItems = SessionItem.merge(local: locals, cloud: clouds, projectNames: projectNames)
        feedSections = FeedSection.group(allItems)
    }

    /// Debounced refresh for watch-event bursts, with a max-latency cap so a
    /// continuous event stream cannot starve the feed forever.
    func scheduleFeedRefresh(after seconds: Double = 5) {
        if Date().timeIntervalSince(lastFeedRefreshAt) > 12 {
            feedRefreshTask?.cancel()
            Task { await refreshFeed() }
            return
        }
        feedRefreshTask?.cancel()
        feedRefreshTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: UInt64(seconds * 1_000_000_000))
            guard !Task.isCancelled else { return }
            await self?.refreshFeed()
        }
    }

    // MARK: Search (the ⌘K overlay)

    func searchQueryChanged() {
        searchTask?.cancel()
        let query = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !query.isEmpty else {
            searchResults = []
            return
        }
        searchTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: 250_000_000)
            guard let self, !Task.isCancelled else { return }
            await self.runSearch(query)
        }
    }

    private func runSearch(_ query: String) async {
        var results = [SessionItem]()
        var seen = Set<String>()

        if let reader = indexReader, let locals = try? reader.search(query, limit: 30) {
            for local in locals {
                let item = allItems.first { $0.clientID == local.sessionID } ?? SessionItem(local: local)
                results.append(item)
                seen.insert(item.clientID)
            }
        }

        if case .signedIn = authState, cloudSyncEnabled {
            let hits = (try? await auth.api.searchSessions(
                query: query, projectIDs: nil, timeFilter: nil, agentNames: nil, limit: 30
            )) ?? []
            for hit in hits {
                guard let clientID = hit.clientId, !seen.contains(clientID) else { continue }
                seen.insert(clientID)
                if let known = allItems.first(where: { $0.clientID == clientID }) {
                    results.append(known)
                } else if let projectID = hit.projectId {
                    results.append(searchHitItem(hit, clientID: clientID, projectID: projectID))
                }
            }
        }

        guard !Task.isCancelled else { return }
        searchResults = results
    }

    /// Minimal item for a cloud search hit we have not merged yet.
    private func searchHitItem(_ hit: SearchHit, clientID: String, projectID: String) -> SessionItem {
        let json = """
        {"id":"\(hit.id)","clientId":"\(clientID)","projectId":"\(projectID)",
         "name":"\(hit.name ?? "Session")","userTitle":\(hit.userTitle.map { "\"\($0)\"" } ?? "null"),
         "createdAt":"","updatedAt":"","metadata":{}}
        """
        if let session = try? JSONDecoder().decode(CloudSession.self, from: Data(json.utf8)) {
            return SessionItem(cloud: session, projectName: hit.project?.name)
        }
        // Decoding a hand-built row only fails if the hit contains quotes;
        // fall back to a bare cloud row via the search title.
        let fallback = IndexedSession(
            sessionID: clientID, provider: "unknown", projectPath: "",
            title: hit.userTitle ?? hit.name ?? "Session", slug: nil,
            createdAt: nil, updatedAt: nil, userPromptCount: nil, markdownPath: nil
        )
        return SessionItem(local: fallback)
    }

    // MARK: Session detail

    func openSession(_ item: SessionItem) {
        selectedSession = item
        detailLoadTask?.cancel()
        detailLoadTask = Task { await loadMarkdown(for: item) }
    }

    func revealSession(id: String) {
        if let item = allItems.first(where: { $0.clientID == id }) {
            openSession(item)
        } else if let live = liveSessions[id] {
            openSession(live.asSessionItem)
        }
    }

    func closeSession() {
        selectedSession = nil
        sessionMarkdown = nil
    }

    func loadMarkdown(for item: SessionItem) async {
        detailLoading = true
        sessionMarkdown = nil
        // A superseded load must not touch the newer session's state.
        defer {
            if selectedSession?.clientID == item.clientID, !Task.isCancelled {
                detailLoading = false
            }
        }

        // Local first: the CLI renders redacted markdown from the provider store.
        if item.origin != .cloudOnly, let projectPath = item.projectPath, let binaryURL {
            let output = try? await CLIRunner.run(
                binary: binaryURL,
                arguments: ["sync", "-s", item.clientID, "--print", "--no-cloud-sync", "--silent"],
                workingDirectory: projectPath, environment: [:], timeout: 60
            )
            guard !Task.isCancelled else { return }
            if let output, !output.isEmpty, selectedSession?.clientID == item.clientID {
                sessionMarkdown = output
                return
            }
        }

        // Cloud fallback. The markdown GET carries no usable ETag semantics
        // server-side, so conditional refresh is client-driven: compare the
        // cached etag against HEAD before re-downloading.
        if let projectID = item.projectID, case .signedIn = authState {
            let cached = markdownCache[item.clientID]
            if let cached, let cachedTag = cached.etag,
               let head = try? await auth.api.sessionHead(projectID: projectID, sessionID: item.clientID),
               head.etag == cachedTag {
                guard selectedSession?.clientID == item.clientID, !Task.isCancelled else { return }
                sessionMarkdown = cached.markdown
                return
            }
            let result = try? await auth.api.sessionMarkdown(
                projectID: projectID, sessionID: item.clientID, etag: nil
            )
            guard selectedSession?.clientID == item.clientID, !Task.isCancelled else { return }
            switch result {
            case .content(let markdown, let etag):
                // The GET drops the ETag header; fetch it from HEAD so the
                // next open can skip the download.
                var storedTag = etag
                if storedTag == nil {
                    storedTag = (try? await auth.api.sessionHead(projectID: projectID, sessionID: item.clientID))?.etag
                }
                markdownCache[item.clientID] = (storedTag, markdown)
                guard selectedSession?.clientID == item.clientID, !Task.isCancelled else { return }
                sessionMarkdown = markdown
            case .notModified:
                sessionMarkdown = cached?.markdown
            case nil:
                sessionMarkdown = cached?.markdown
            }
            if sessionMarkdown != nil { return }
        }

        sessionMarkdown = nil
    }

    func friendlyCloudError(_ error: Error) -> String {
        if let apiError = error as? CloudAPIError {
            switch apiError {
            case .unauthorized: return "Your session expired. Sign in again to reconnect Cloud."
            case .forbidden: return "Your plan does not include this feature."
            case .network: return "SpecStory Cloud is unreachable right now."
            default: break
            }
        }
        return "Could not reach SpecStory Cloud."
    }
}
