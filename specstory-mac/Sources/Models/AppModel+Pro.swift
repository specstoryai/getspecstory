import AppKit
import Foundation
import SwiftUI
import SpecStoryKit

/// Pro surface state: plan, feature gates, the skills library, and billing
/// actions. Owned by AppModel as a plain stored sub-model (`let pro =
/// ProModel()` in AppModel.swift); views observe it directly so Pro churn
/// does not invalidate the whole god-model.
///
/// Integration wiring (see feature notes):
///   1. AppModel.swift: add `let pro = ProModel()` to the Services section.
///   2. AppModel.bootstrap(): call `pro.configure(auth: auth)` right after
///      `auth.bootstrap()`, and add `Task { await pro.refresh(signedIn: authState != .signedOut) }`
///      next to the existing `Task { await refreshGates() }`.
///   3. AppModel+Auth.swift: in completeSignIn, signOut, and handleAuthChange,
///      add `Task { await pro.refresh(signedIn: authState != .signedOut) }`.
@MainActor
final class ProModel: ObservableObject {
    // MARK: Gate state

    @Published private(set) var plan: Plan = .free
    @Published private(set) var entitlementFeatures: [String: Bool] = [:]
    @Published private(set) var flags: [String: Bool] = [:]
    @Published private(set) var gatesLoaded = false

    // MARK: Skills state

    @Published private(set) var skills: [SkillRow] = []
    @Published private(set) var skillsLoading = false
    @Published private(set) var skillsError: String?
    @Published private(set) var skillsLoadedOnce = false
    /// The skill whose detail sheet is open, if any.
    @Published var selectedSkill: SkillRow?
    /// Dossier ids with a Keep or Dismiss in flight.
    @Published private(set) var reviewBusyDossierIDs: Set<String> = []
    /// Skill names with an install-state PATCH in flight.
    @Published private(set) var installBusySkillNames: Set<String> = []

    // MARK: Runs state

    @Published private(set) var skillRuns: [SkillRun] = []
    @Published private(set) var runsLoading = false
    @Published private(set) var runsError: String?
    @Published private(set) var runsLoadedOnce = false
    @Published private(set) var runTriggerBusy = false

    private var runPollTask: Task<Void, Never>?

    // MARK: Billing state

    @Published private(set) var billingBusy = false

    private var api: CloudProAPI?

    /// Pro cloud search (search/resume) for the ⌘K blend; nil when the gate
    /// is not enabled so callers fall back to GraphQL search.
    func resumeSearch(query: String, projectID: String?) async -> [ResumeSearchHit]? {
        guard resumeGate == .enabled, let api else { return nil }
        return (try? await api.searchResume(query: query, projectID: projectID)) ?? []
    }
    private(set) var signedIn = false

    // MARK: Configuration

    /// Wire the API client to the app's AuthManager. Called once from
    /// AppModel.bootstrap(); safe to call again (e.g. in previews).
    func configure(auth: AuthManager) {
        api = CloudProAPI(
            baseURL: AppModel.cloudBaseURL,
            accessTokenProvider: { [auth] in try await auth.validAccessToken() }
        )
    }

    /// Refetch flags and entitlement. Flags fail open; entitlement fails
    /// closed, so any fetch failure reads as the free plan.
    func refresh(signedIn: Bool) async {
        self.signedIn = signedIn
        guard signedIn, let api else {
            plan = .free
            entitlementFeatures = [:]
            flags = [:]
            gatesLoaded = false
            skills = []
            skillsLoadedOnce = false
            skillsError = nil
            selectedSkill = nil
            skillRuns = []
            runsLoadedOnce = false
            runsError = nil
            stopRunPolling()
            return
        }
        flags = await api.flags()
        if let entitlement = try? await api.entitlement() {
            entitlementFeatures = entitlement.features
            plan = Plan(planString: entitlement.plan)
        } else {
            entitlementFeatures = [:]
            plan = .free
        }
        gatesLoaded = true
    }

    // MARK: Gates

    var skillsGate: FeatureGate { gate(for: "skills") }
    var resumeGate: FeatureGate { gate(for: "resume") }
    var analyticsGate: FeatureGate { gate(for: "analytics") }

    private func gate(for feature: String) -> FeatureGate {
        FeatureGate.gate(flagOn: flags[feature], entitled: entitlementFeatures[feature] ?? false)
    }

    // MARK: Skills

    func refreshSkills() async {
        guard let api, !skillsLoading else { return }
        skillsLoading = true
        skillsError = nil
        do {
            skills = try await api.skillsLibrary()
            skillsLoadedOnce = true
            // Keep an open detail sheet in sync with fresh data.
            if let selected = selectedSkill {
                selectedSkill = skills.first { $0.name == selected.name } ?? selectedSkill
            }
        } catch {
            if clearsRowsOnFailure(error) { skills = [] }
            skillsError = degrade(error, signInMessage: "Sign in to SpecStory Cloud to see your skills.")
        }
        skillsLoading = false
    }

    /// Keep: approve the review dossier behind this skill. The server forges
    /// the skill on approval, so a refresh moves the row to Ready.
    func keepSkill(_ skill: SkillRow) async {
        await review(skill, action: .approve, note: nil)
    }

    /// Dismiss: decline the review dossier. The web client sends an empty
    /// note; there is no other delete anywhere on this surface.
    func dismissSkill(_ skill: SkillRow) async {
        await review(skill, action: .decline, note: "")
        if selectedSkill?.name == skill.name {
            selectedSkill = nil
        }
    }

    private func review(_ skill: SkillRow, action: SkillDossierAction, note: String?) async {
        guard let api, let dossierID = skill.dossierId, !dossierID.isEmpty,
              !reviewBusyDossierIDs.contains(dossierID) else { return }
        reviewBusyDossierIDs.insert(dossierID)
        do {
            try await api.reviewDossier(id: dossierID, action: action, note: note)
            await refreshSkills()
        } catch {
            skillsError = degrade(error, signInMessage: "Sign in to SpecStory Cloud to review skills.")
        }
        reviewBusyDossierIDs.remove(dossierID)
    }

    /// Flip the server-side install state for a skill (available <-> installed).
    func setSkillInstalled(_ skill: SkillRow, installed: Bool) async {
        guard let api, !installBusySkillNames.contains(skill.name) else { return }
        installBusySkillNames.insert(skill.name)
        do {
            try await api.setSkillInstallState(name: skill.name, installed: installed)
            await refreshSkills()
        } catch {
            skillsError = degrade(error, signInMessage: "Sign in to SpecStory Cloud to manage installs.")
        }
        installBusySkillNames.remove(skill.name)
    }

    /// The CLI command that installs a skill to disk, shared with the web UI.
    func installCommand(for skill: SkillRow) -> String {
        "specstory skills install \(skill.name)"
    }

    func copyInstallCommand(_ skill: SkillRow) {
        copyToPasteboard(installCommand(for: skill))
    }

    func copySkillContent(_ skill: SkillRow) {
        copyToPasteboard(skill.content)
    }

    private func copyToPasteboard(_ string: String) {
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(string, forType: .string)
    }

    // MARK: Runs

    var anySkillRunInProgress: Bool {
        skillRuns.contains { $0.isInProgress }
    }

    /// Fetch the run list (list and detail in one). Quiet refreshes come from
    /// the 4 s live poll and skip the loading spinner, matching the web UI.
    func refreshRuns(quiet: Bool = false) async {
        guard let api, !runsLoading else { return }
        if !quiet { runsLoading = true }
        do {
            skillRuns = try await api.skillRuns()
            runsLoadedOnce = true
            runsError = nil
        } catch {
            if clearsRowsOnFailure(error) { skillRuns = [] }
            runsError = degrade(error, signInMessage: "Sign in to SpecStory Cloud to see your skill runs.")
        }
        if !quiet { runsLoading = false }
        scheduleRunPollingIfNeeded()
    }

    /// Start a background skills run. A 409 (another run already live) is
    /// benign: the refresh right after shows the running one.
    func triggerSkillsRun() async {
        guard let api, !runTriggerBusy else { return }
        runTriggerBusy = true
        runsError = nil
        do {
            _ = try await api.triggerSkillRun()
        } catch let error as CloudProError {
            switch error {
            case .runAlreadyInProgress:
                break
            case .upgradeRequired:
                // Could be entitlement drift or the environment kill switch;
                // keep the gate untouched and just say runs are unavailable.
                runsError = "Skill runs are disabled right now. Please try again later."
            case .unauthorized:
                runsError = "Sign in to SpecStory Cloud to start a run."
            default:
                runsError = error.localizedDescription
            }
        } catch {
            runsError = error.localizedDescription
        }
        await refreshRuns(quiet: true)
        runTriggerBusy = false
    }

    /// Poll every 4 s while any run is queued, sharding, running, or judging;
    /// stop when idle. When the last live run finishes, refresh the library
    /// once so freshly forged skills appear.
    private func scheduleRunPollingIfNeeded() {
        guard signedIn, anySkillRunInProgress else { return }
        guard runPollTask == nil else { return }
        runPollTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 4_000_000_000)
                guard let self, !Task.isCancelled else { return }
                guard self.signedIn, self.anySkillRunInProgress else { break }
                await self.refreshRuns(quiet: true)
            }
            guard let self, !Task.isCancelled else { return }
            self.runPollTask = nil
            if self.skillsGate == .enabled {
                await self.refreshSkills()
            }
        }
    }

    /// Called when the Skills panel disappears so the app does not poll
    /// invisibly forever; refreshRuns rearms it on the next appearance.
    func stopRunPolling() {
        runPollTask?.cancel()
        runPollTask = nil
    }

    // MARK: Shared error mapping

    /// Gate and auth failures invalidate the rows; transient network or
    /// server errors keep stale rows visible under the error message.
    private func clearsRowsOnFailure(_ error: Error) -> Bool {
        guard let proError = error as? CloudProError else { return false }
        switch proError {
        case .featureDark, .upgradeRequired, .unauthorized: return true
        default: return false
        }
    }

    /// Fold the two expected degrade states into the gates (dark folds into
    /// the flag, upgrade folds into the entitlement) and translate the rest
    /// into a user-facing message. Returns nil when the gate absorbed it.
    private func degrade(_ error: Error, signInMessage: String) -> String? {
        guard let proError = error as? CloudProError else {
            return error.localizedDescription
        }
        switch proError {
        case .featureDark:
            flags["skills"] = false
            return nil
        case .upgradeRequired:
            entitlementFeatures["skills"] = false
            return nil
        case .unauthorized:
            return signInMessage
        default:
            return proError.localizedDescription
        }
    }

    // MARK: Billing

    static var billingPageURL: URL {
        AppModel.cloudBaseURL.appendingPathComponent("billing")
    }

    /// Opens Stripe Checkout for the plan in the browser. Falls back to the
    /// cloud billing page whenever the checkout endpoint is unavailable
    /// (signed out, billing flag dark, network trouble).
    func openCheckout(plan targetPlan: Plan = .pro) {
        guard !billingBusy else { return }
        billingBusy = true
        Task {
            var destination = Self.billingPageURL
            if let api {
                if let result = try? await api.checkout(plan: targetPlan) {
                    if let url = result.url {
                        destination = url
                    } else if result.alreadySubscribed,
                              let portal = try? await api.billingPortalURL() {
                        destination = portal
                    }
                }
            }
            NSWorkspace.shared.open(destination)
            billingBusy = false
        }
    }

    /// Opens the Stripe Customer Portal, falling back to the cloud billing
    /// page when the user has no billing account yet.
    func openPortal() {
        guard !billingBusy else { return }
        billingBusy = true
        Task {
            var destination = Self.billingPageURL
            if let api, let portal = try? await api.billingPortalURL() {
                destination = portal
            }
            NSWorkspace.shared.open(destination)
            billingBusy = false
        }
    }

    // MARK: Sample fixtures (gated preview)

    /// Realistic library rows rendered blurred behind the upgrade card so
    /// non-Pro users see the shape of the feature (Granola pattern). Never
    /// mixed into live state.
    static let sampleSkills: [SkillRow] = [
        SkillRow(
            name: "workflow-fan-out-review",
            description: "When a workflow fans out to parallel agents, review the merged result against each brief before shipping",
            content: """
            # workflow-fan-out-review

            After a fan-out run completes, diff every subagent's output against \
            its original brief. Merge only the pieces that satisfy the brief; \
            send the rest back with a narrower prompt.
            """,
            state: "review",
            updatedAt: "2026-07-30T18:12:00Z",
            confidence: 0.64,
            verdict: "needs-edits",
            dossierId: "d-2041",
            clusterKey: "fanout-review",
            evidenceRefs: ["2026-07-12_wf-orchestrator", "2026-07-19_parallel-port", "2026-07-24_merge-pass"]
        ),
        SkillRow(
            name: "prove-it-harness-setup",
            description: "When a fix claims to work, wire a reproducible harness that proves it before merging",
            content: """
            # prove-it harness setup

            Before accepting any bugfix, build the smallest script that \
            reproduces the failure, run it red, apply the fix, run it green, \
            and commit the harness beside the fix.

            ```zsh
            ./scripts/repro.sh && echo "still broken" || echo "fixed"
            ```
            """,
            state: "ready",
            updatedAt: "2026-07-28T16:40:00Z",
            confidence: 0.91,
            verdict: "confirmed",
            clusterKey: "harness-proof",
            evidenceRefs: ["2026-07-08_flaky-watcher", "2026-07-15_auth-race", "2026-07-21_fts-crash", "2026-07-26_cli-timeout"]
        ),
        SkillRow(
            name: "granola-window-audit",
            description: "A latent habit: auditing window chrome against the reference app before every UI review",
            content: """
            # granola-window-audit

            Before screenshots go out, compare paddings, hairlines, and serif \
            headers against the reference shell side by side.
            """,
            state: "ready",
            updatedAt: "2026-07-27T09:05:00Z",
            confidence: 0.71,
            verdict: "confirmed",
            clusterKey: "ui-audit-theme",
            evidenceRefs: ["2026-07-18_shell-polish", "2026-07-22_feed-cards"],
            kind: "theme"
        ),
        SkillRow(
            name: "vendored-cli-refresh",
            description: "When the vendored CLI drifts from HEAD, rebuild it and repin the manifest before touching app code",
            content: """
            # vendored-cli-refresh

            Run the vendor script, verify the manifest sha, and only then \
            debug app behavior. Half of all "app bugs" were stale binaries.
            """,
            state: "installed",
            updatedAt: "2026-07-24T14:30:00Z",
            installedAgents: ["claude", "codex"],
            confidence: 0.88,
            verdict: "confirmed",
            clusterKey: "vendor-pin",
            evidenceRefs: ["2026-07-05_manifest-pin", "2026-07-11_stale-binary", "2026-07-20_arch-switch"]
        ),
        SkillRow(
            name: "session-secret-sweep",
            description: "Before committing session history, sweep it for tokens and keys with the guard hook",
            content: """
            # session-secret-sweep

            Install the pre-commit guard on every repo that stores history. \
            Never rely on eyeballing a diff to catch a pasted token.
            """,
            state: "installed",
            updatedAt: "2026-07-19T11:20:00Z",
            installedAgents: ["claude"],
            confidence: 0.84,
            verdict: "confirmed",
            clusterKey: "secret-guard",
            evidenceRefs: ["2026-07-03_guard-hook", "2026-07-14_token-scare"]
        ),
    ]

    /// Sample run activity for the same preview: a fresh manual run, an
    /// instant nothing-new run, and an older reconciled failure.
    static let sampleRuns: [SkillRun] = [
        SkillRun(
            id: "run-7f3a2c",
            status: "done",
            trigger: "manual",
            shardCount: 2,
            shardsDone: 2,
            sessionCount: 41,
            spanCount: 356,
            watermarkFrom: "2026-07-21T00:00:00Z",
            watermarkTo: "2026-07-30T17:40:00Z",
            createdAt: "2026-07-30T17:41:00Z",
            startedAt: "2026-07-30T17:41:05Z",
            endedAt: "2026-07-30T17:54:30Z",
            updatedAt: "2026-07-30T17:54:30Z",
            sessionsMined: 41,
            shards: [
                SkillRunShard(shardKey: "getspecstory", scopeKind: "project",
                              projectIDs: ["getspecstory"], status: "done", sandboxId: "sbx-91ka",
                              sessionCount: 27, spanCount: 240,
                              startedAt: "2026-07-30T17:41:10Z", endedAt: "2026-07-30T17:51:00Z"),
                SkillRunShard(shardKey: "bucket:misc", scopeKind: "bucket",
                              projectIDs: ["dotfiles", "blog", "sandbox"], status: "done", sandboxId: "sbx-42fe",
                              sessionCount: 14, spanCount: 116,
                              startedAt: "2026-07-30T17:41:12Z", endedAt: "2026-07-30T17:49:20Z"),
            ],
            dossierVerdicts: ["confirmed": 2, "needs-edits": 1, "unverified": 1],
            dossierTotal: 4,
            dossiers: [
                SkillRunDossier(name: "prove-it-harness-setup", clusterKey: "harness-proof",
                                verdict: "confirmed", confidence: 0.91, hasSkill: true),
                SkillRunDossier(name: "granola-window-audit", clusterKey: "ui-audit-theme",
                                verdict: "confirmed", confidence: 0.71, adjudicated: true, hasSkill: true),
                SkillRunDossier(name: "workflow-fan-out-review", clusterKey: "fanout-review",
                                verdict: "needs-edits", confidence: 0.64, hasSkill: true),
                SkillRunDossier(name: "notification-triage", clusterKey: "notify-triage",
                                verdict: "unverified", confidence: 0.35, hasSkill: false),
            ]
        ),
        SkillRun(
            id: "run-1d9b04",
            status: "done",
            trigger: "nudge",
            shardCount: 0,
            shardsDone: 0,
            createdAt: "2026-07-29T08:02:00Z",
            startedAt: "2026-07-29T08:02:00Z",
            endedAt: "2026-07-29T08:02:01Z",
            updatedAt: "2026-07-29T08:02:01Z"
        ),
        SkillRun(
            id: "run-c55e18",
            status: "failed",
            trigger: "cron",
            shardCount: 1,
            shardsDone: 0,
            createdAt: "2026-07-26T02:00:00Z",
            startedAt: "2026-07-26T02:00:04Z",
            updatedAt: "2026-07-26T02:33:00Z",
            error: "reconciled: stuck past TTL",
            sessionsMined: 12,
            shards: [
                SkillRunShard(shardKey: "__all__", scopeKind: "owner",
                              projectIDs: [], status: "failed", sandboxId: "sbx-0aa1",
                              sessionCount: 12, spanCount: 98,
                              startedAt: "2026-07-26T02:00:10Z",
                              error: "sandbox idle past watchdog"),
            ]
        ),
    ]

    // MARK: Upsell copy

    static let skillsUpsell = UpsellCopy(
        icon: "wand.and.stars",
        title: "Skills come with SpecStory Pro",
        blurb: "SpecStory mines your synced sessions into reusable skills you can install in every coding agent.",
        bullets: [
            "Skill candidates mined from your real sessions",
            "One-click install into Claude Code, Codex, Cursor, and more",
            "Drift badges keep installed skills current",
        ]
    )

    static let resumeUpsell = UpsellCopy(
        icon: "arrow.uturn.forward",
        title: "Cloud resume comes with SpecStory Pro",
        blurb: "Pick up any synced session from any of your machines and relaunch it in your coding agent.",
        bullets: [
            "Resume sessions synced from your other machines",
            "Search every synced session, not just this Mac",
            "Local resume stays free, always",
        ]
    )

    static let analyticsUpsell = UpsellCopy(
        icon: "chart.bar.xaxis",
        title: "Analytics come with SpecStory Pro",
        blurb: "See how you work across agents: activity, time of day, conversation depth, estimated spend, and Spec Score.",
        bullets: [
            "Cross-agent activity and streaks",
            "Estimated spend with cache savings",
            "Spec Score grades your prompts",
        ]
    )
}
