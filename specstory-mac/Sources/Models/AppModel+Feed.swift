import Foundation
import SpecStoryKit

extension AppModel {
    // MARK: Feed assembly (local + cloud merge)

    func refreshFeed() async {
        lastFeedRefreshAt = Date()
        localTotalSessions = await indexBox.count()
        var locals = await indexBox.recentSessions(limit: 500)

        // Reindex rebuilds the database from scratch; a read that lands
        // mid-rebuild sees emptiness that must not blow away a good feed.
        if locals.isEmpty, reindexInFlight, !allItems.isEmpty {
            locals = allItems.compactMap { item in
                guard item.origin != .cloudOnly, let path = item.projectPath else { return nil }
                return IndexedSession(
                    sessionID: item.clientID, provider: item.provider, projectPath: path,
                    title: item.title, slug: nil, createdAt: item.createdAt,
                    updatedAt: item.updatedAt, userPromptCount: item.promptCount, markdownPath: nil
                )
            }
        }

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
                cloudFailureStreak = 0
                cloudReachable = true
                // The projects listing carries per-project session counts;
                // their sum is the true synced total, not a page size.
                let counts = projects.compactMap(\.sessionCount)
                cloudSyncedTotal = counts.isEmpty ? nil : counts.reduce(0, +)
                let sizes = projects.compactMap(\.totalMarkdownSize)
                cloudSyncedBytes = sizes.isEmpty ? nil : sizes.reduce(0, +)
            } catch {
                // Transient blips (sleep wake, brief offline, the auth
                // manager's two-minute network cooldown) should not paint
                // the banner: stay quiet on the first failure and retry
                // shortly; surface it only when trouble persists.
                cloudFailureStreak += 1
                if cloudFailureStreak >= 2 {
                    cloudError = friendlyCloudError(error)
                    cloudReachable = false
                }
                scheduleFeedRefresh(after: 45)
            }
            // The recent head is fast; the full per-project sweep fills in
            // everything else behind it.
            scheduleCloudSweep()
        }

        // Sessions beyond the recent head, from the last full sweep.
        let sweep = cloudSweepSessions.values.filter { session in
            !clouds.contains { $0.clientId == session.clientId }
        }
        clouds.append(contentsOf: sweep)

        latestLocals = locals
        latestProjectNames = projectNames
        allItems = SessionItem.merge(local: locals, cloud: clouds, projectNames: projectNames)
        rebuildFeedSections()
    }

    /// Cloud parity browsing: every synced session across every project, not
    /// just the recent head. Sweeps are bounded (4 projects at a time) and
    /// throttled to one per five minutes.
    private func scheduleCloudSweep() {
        guard Date().timeIntervalSince(lastCloudSweepAt) > 300, !cloudSweepRunning else { return }
        cloudSweepRunning = true
        let projects = cloudProjects
        Task { [weak self] in
            guard let self else { return }
            var collected = [String: CloudSession]()
            var index = 0
            while index < projects.count {
                let batch = Array(projects[index..<min(index + 4, projects.count)])
                await withTaskGroup(of: [CloudSession].self) { group in
                    for project in batch {
                        group.addTask { [auth = self.auth] in
                            (try? await auth.api.sessions(projectID: project.id, limit: 200)) ?? []
                        }
                    }
                    for await sessions in group {
                        for session in sessions {
                            collected[session.clientId] = session
                        }
                    }
                }
                index += batch.count
            }
            self.cloudSweepSessions = collected
            self.lastCloudSweepAt = Date()
            self.cloudSweepRunning = false
            // A full sweep makes the cross-machine number real.
            self.otherMachineTotal = collected.values.filter {
                $0.metadata.deviceId != nil && $0.metadata.deviceId != DeviceIdentity.current
            }.count
            // Remerge with the sweep's fuller picture.
            self.allItems = SessionItem.merge(
                local: self.latestLocals, cloud: Array(collected.values),
                projectNames: self.latestProjectNames
            )
            self.rebuildFeedSections()
        }
    }

    func rebuildFeedSections() {
        feedSections = FeedSection.build(allItems, grouping: feedGrouping, sort: feedSort)
    }

    // MARK: Section collapse

    func reloadCollapsedSections() {
        collapsedSections = Set(UserDefaults.standard.stringArray(forKey: "collapsedSections.\(feedGrouping.rawValue)") ?? [])
    }

    func isSectionCollapsed(_ section: FeedSection) -> Bool {
        collapsedSections.contains(section.title)
    }

    func toggleSection(_ section: FeedSection) {
        if collapsedSections.contains(section.title) {
            collapsedSections.remove(section.title)
        } else {
            collapsedSections.insert(section.title)
        }
    }

    func collapseAllSections() {
        collapsedSections = Set(feedSections.map(\.title))
    }

    func expandAllSections() {
        collapsedSections = []
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

    // MARK: Search (the ⌘K overlay, CLI session-search parity)

    /// The CLI's minimum: two letters or digits before search fires.
    func queryReady(_ text: String) -> Bool {
        text.unicodeScalars.filter { $0.properties.isAlphabetic || ($0.value >= 48 && $0.value <= 57) }.count >= 2
    }

    func searchQueryChanged() {
        searchTask?.cancel()
        cloudSearchTask?.cancel()
        searchSeq += 1
        let seq = searchSeq
        let query = searchMention.text.trimmingCharacters(in: .whitespacesAndNewlines)

        // CLI parity: every keystroke drops prior cloud rows so stale-query
        // hits never blend with fresh local results.
        cloudSearchRows = []

        guard queryReady(query) || searchMention.hasChips else {
            localSearchRows = []
            searchResults = []
            searchingLocal = false
            searchingCloud = false
            return
        }

        // Local FTS on a short debounce; cloud fires on a typing pause. The
        // debounce is long enough that queued index queries cannot pile up
        // behind a fast typist on a large database.
        searchingLocal = true
        searchingCloud = case1SignedIn()
        searchTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: 180_000_000)
            guard !Task.isCancelled else { return }
            await self?.runLocalSearch(query, seq: seq)
        }
        cloudSearchTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: 600_000_000)
            guard !Task.isCancelled else { return }
            await self?.runCloudSearch(query, seq: seq)
        }
    }

    private func case1SignedIn() -> Bool {
        if case .signedIn = authState, cloudSyncEnabled { return true }
        return false
    }

    /// The overlay's resting content: most recent sessions, palette style.
    var recentSearchRows: [SearchResultRow] {
        allItems.prefix(8).map { SearchResultRow(item: $0, snippet: nil) }
    }

    private func runLocalSearch(_ query: String, seq: Int) async {
        // Phase 1: rows without snippets render fast; phase 2 attaches
        // matching context. snippet() over a multi-gigabyte FTS table is the
        // expensive half, so it must never block first paint.
        if queryReady(query) {
            let fast = await indexBox.searchWithSnippets(query, limit: 500, snippetLimit: 0)
            guard searchSeq == seq else { return }
            localSearchRows = applyChipFilters(fast.map { hit in
                SearchResultRow(
                    item: allItems.first { $0.clientID == hit.session.sessionID } ?? SessionItem(local: hit.session),
                    snippet: nil
                )
            })
            searchingLocal = false
            blendSearchResults()

            let withSnippets = await indexBox.searchWithSnippets(query, limit: 30, snippetLimit: 12)
            guard searchSeq == seq else { return }
            var snippetByID = [String: HighlightedSnippet]()
            for hit in withSnippets {
                if let raw = hit.snippet {
                    snippetByID[hit.session.sessionID] = HighlightedSnippet.parse(raw)
                }
            }
            localSearchRows = localSearchRows.map { row in
                guard let snippet = snippetByID[row.item.clientID] else { return row }
                return SearchResultRow(item: row.item, snippet: snippet)
            }
        } else {
            guard searchSeq == seq else { return }
            localSearchRows = applyChipFilters([])
            searchingLocal = false
        }
        blendSearchResults()
    }

    private func runCloudSearch(_ query: String, seq: Int) async {
        guard case .signedIn = authState, cloudSyncEnabled else { return }
        var rows = [SearchResultRow]()

        if let hits = await pro.resumeSearch(query: query, projectID: searchMention.selectedProjectIDs.first) {
            // Pro path: server-side FTS with STX/ETX snippets (CLI parity).
            for hit in hits {
                // CLI parity: hits whose agent cannot be resolved are dropped.
                guard let provider = Provider(providerID: hit.metadata.agentName ?? "") else { continue }
                let cloud = CloudSession(
                    id: hit.id, clientId: hit.clientId, projectId: hit.projectId,
                    name: hit.name, userTitle: hit.userTitle,
                    createdAt: hit.createdAt, updatedAt: hit.updatedAt,
                    startedAt: hit.startedAt, endedAt: hit.endedAt,
                    sessionDataSize: hit.sessionDataSize,
                    metadata: CloudSessionMetadata(
                        agentName: provider.rawValue,
                        deviceId: hit.metadata.deviceId,
                        machineName: hit.metadata.machineName,
                        title: hit.metadata.title
                    )
                )
                rows.append(SearchResultRow(
                    item: SessionItem(cloud: cloud, projectName: hit.projectName),
                    snippet: hit.snippet
                ))
            }
        } else if queryReady(query) {
            // Free tier: GraphQL search, no snippets.
            let hits = (try? await auth.api.searchSessions(
                query: query,
                projectIDs: searchMention.projectIDsParam,
                timeFilter: searchMention.timeFilterParam,
                agentNames: searchMention.agentNamesParam,
                limit: 30
            )) ?? []
            for hit in hits {
                guard let clientID = hit.clientId else { continue }
                if let known = allItems.first(where: { $0.clientID == clientID }) {
                    rows.append(SearchResultRow(item: known, snippet: nil))
                } else if let projectID = hit.projectId {
                    rows.append(SearchResultRow(item: searchHitItem(hit, clientID: clientID, projectID: projectID), snippet: nil))
                }
            }
        }

        guard searchSeq == seq else { return }
        cloudSearchRows = applyChipFilters(rows)
        searchingCloud = false
        blendSearchResults()
    }

    /// Mention chips filter both legs client-side (the CLI applies agent and
    /// machine filters after its merge too). Project chips scope the cloud
    /// query server-side; locals match by project folder name.
    private func applyChipFilters(_ rows: [SearchResultRow]) -> [SearchResultRow] {
        var filtered = rows
        if !searchMention.selectedAgents.isEmpty {
            let providers = Set(searchMention.selectedAgents.compactMap { Provider(providerID: $0)?.rawValue })
            filtered = filtered.filter { providers.contains(Provider(providerID: $0.item.provider)?.rawValue ?? "") }
        }
        if let window = timeWindow(searchMention.timeFilter) {
            filtered = filtered.filter { ($0.item.updatedAt ?? $0.item.createdAt).map { $0 >= window } ?? false }
        }
        if !searchMention.selectedProjectIDs.isEmpty {
            let ids = Set(searchMention.selectedProjectIDs)
            let names = Set(cloudProjects.filter { ids.contains($0.id) }.map { $0.name.lowercased() })
            filtered = filtered.filter { row in
                if let projectID = row.item.projectID { return ids.contains(projectID) }
                return names.contains((row.item.projectName ?? "").lowercased())
            }
        }
        return filtered
    }

    /// Minimal item for a GraphQL hit not present in the merged feed yet.
    private func searchHitItem(_ hit: SearchHit, clientID: String, projectID: String) -> SessionItem {
        let cloud = CloudSession(
            id: hit.id, clientId: clientID, projectId: projectID,
            name: hit.name ?? "Session", userTitle: hit.userTitle
        )
        return SessionItem(cloud: cloud, projectName: hit.project?.name)
    }

    private func timeWindow(_ filter: String?) -> Date? {
        switch filter {
        case "today": return Calendar.current.startOfDay(for: Date())
        case "week": return Calendar.current.date(byAdding: .day, value: -7, to: Date())
        case "month": return Calendar.current.date(byAdding: .month, value: -1, to: Date())
        default: return nil
        }
    }

    /// CLI merge semantics (session_tui_cloud.go mergeCloudRows): dedupe by
    /// provider + session id with local winning, then one stable recency sort
    /// of the union by parsed timestamps.
    private func blendSearchResults() {
        var seen = Set<String>()
        var union = [SearchResultRow]()
        for row in localSearchRows {
            let key = (Provider(providerID: row.item.provider)?.rawValue ?? row.item.provider) + "\u{0}" + row.item.clientID
            if seen.insert(key).inserted { union.append(row) }
        }
        for row in cloudSearchRows {
            let key = (Provider(providerID: row.item.provider)?.rawValue ?? row.item.provider) + "\u{0}" + row.item.clientID
            if seen.insert(key).inserted { union.append(row) }
        }
        searchResults = union.sorted { $0.item.sortDate > $1.item.sortDate }
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
