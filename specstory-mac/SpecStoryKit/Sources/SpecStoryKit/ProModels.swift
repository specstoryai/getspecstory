import Foundation

// MARK: - Plans

/// SpecStory Cloud subscription plans, ranked. Unknown or missing plan strings
/// decode as .free so entitlement display fails closed.
public enum Plan: String, CaseIterable, Equatable, Sendable {
    case free
    case pro
    case team
    case enterprise

    public var displayName: String {
        switch self {
        case .free: return "Free"
        case .pro: return "Pro"
        case .team: return "Team"
        case .enterprise: return "Enterprise"
        }
    }

    /// Rank for min-plan comparisons (free 0 ... enterprise 3).
    public var rank: Int {
        switch self {
        case .free: return 0
        case .pro: return 1
        case .team: return 2
        case .enterprise: return 3
        }
    }

    /// Tolerant constructor for server plan strings; unknown reads as free.
    public init(planString: String?) {
        let normalized = planString?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        self = normalized.flatMap { Plan(rawValue: $0) } ?? .free
    }
}

// MARK: - Feature gate

/// The VSIX-shared triple state for a Pro surface: fully on, visible with an
/// upgrade prompt, or absent from the UI entirely (dark-launched).
public enum FeatureGate: Equatable, Hashable, Sendable {
    case enabled
    case upsell
    case hidden

    /// Combinator matching the web/VSIX layering: the environment flag sits
    /// above the entitlement. Flags fail open (a missing flag counts as on);
    /// entitlement fails closed (only an explicit true enables).
    public static func gate(flagOn: Bool?, entitled: Bool) -> FeatureGate {
        if flagOn == false { return .hidden }
        return entitled ? .enabled : .upsell
    }
}

// MARK: - Skills

/// One row of the unified skills library (GET /api/v1/lore/skills/library).
/// The cloud payload carries more fields than the app needs; decoding is
/// tolerant and maps trigger -> description and skillMd -> content.
public struct SkillRow: Identifiable, Equatable, Sendable {
    public let name: String
    public let description: String?
    /// Full SKILL.md markdown, inlined by the library endpoint.
    public let content: String
    /// review | ready | installed (furthest state wins server-side).
    public let state: String?
    public let updatedAt: String?
    public let installedAgents: [String]?
    public let confidence: Double?
    public let verdict: String?
    public let recommendation: String?
    public let dossierId: String?
    /// Provenance: which mined cluster this skill was forged from.
    public let clusterKey: String?
    /// Up to 5 session references backing the skill.
    public let evidenceRefs: [String]?
    /// "theme" marks a latent practice mined from conversational sessions;
    /// "corr" (or nil) is a corroborated workflow.
    public let kind: String?

    public var id: String { name }
    public var updatedAtDate: Date? { CloudDate.parse(updatedAt) }
    public var isLatentTheme: Bool { kind == "theme" }

    public init(name: String, description: String? = nil, content: String = "",
                state: String? = nil, updatedAt: String? = nil, installedAgents: [String]? = nil,
                confidence: Double? = nil, verdict: String? = nil,
                recommendation: String? = nil, dossierId: String? = nil,
                clusterKey: String? = nil, evidenceRefs: [String]? = nil, kind: String? = nil) {
        self.name = name
        self.description = description
        self.content = content
        self.state = state
        self.updatedAt = updatedAt
        self.installedAgents = installedAgents
        self.confidence = confidence
        self.verdict = verdict
        self.recommendation = recommendation
        self.dossierId = dossierId
        self.clusterKey = clusterKey
        self.evidenceRefs = evidenceRefs
        self.kind = kind
    }
}

extension SkillRow: Decodable {
    private enum CodingKeys: String, CodingKey {
        case name, description, trigger, content, skillMd, state
        case updatedAt, createdAt, installedAgents, confidence, verdict
        case recommendation, dossierId, clusterKey, evidenceRefs, kind
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        description = try c.decodeIfPresent(String.self, forKey: .description)
            ?? c.decodeIfPresent(String.self, forKey: .trigger)
        content = try c.decodeIfPresent(String.self, forKey: .content)
            ?? c.decodeIfPresent(String.self, forKey: .skillMd) ?? ""
        state = try c.decodeIfPresent(String.self, forKey: .state)
        updatedAt = try c.decodeIfPresent(String.self, forKey: .updatedAt)
            ?? c.decodeIfPresent(String.self, forKey: .createdAt)
        installedAgents = try? c.decodeIfPresent([String].self, forKey: .installedAgents)
        // Confidence can arrive as Double or Int depending on the SQL path.
        if let d = try? c.decodeIfPresent(Double.self, forKey: .confidence) {
            confidence = d
        } else if let i = try? c.decodeIfPresent(Int.self, forKey: .confidence) {
            confidence = Double(i)
        } else {
            confidence = nil
        }
        verdict = try c.decodeIfPresent(String.self, forKey: .verdict)
        recommendation = try c.decodeIfPresent(String.self, forKey: .recommendation)
        dossierId = try c.decodeIfPresent(String.self, forKey: .dossierId)
        clusterKey = try? c.decodeIfPresent(String.self, forKey: .clusterKey)
        evidenceRefs = try? c.decodeIfPresent([String].self, forKey: .evidenceRefs)
        kind = try? c.decodeIfPresent(String.self, forKey: .kind)
    }
}

// MARK: - Skill runs (GET /api/v1/lore/runs)

/// Lifecycle phase of a background skills run or one of its shards. Run
/// statuses: queued | sharding | running | judging | reducing then done |
/// failed. Shards add pending | claimed. Unknown strings map to .unknown so
/// new server phases degrade to a quiet chip instead of a decode failure.
public enum SkillRunPhase: String, Equatable, Sendable {
    case queued, sharding, running, judging, reducing, done, failed
    case pending, claimed
    case unknown

    public init(status: String?) {
        let normalized = status?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() ?? ""
        self = SkillRunPhase(rawValue: normalized) ?? .unknown
    }

    /// The four statuses that gate the Run now button and the 4 s live poll,
    /// mirroring the web client's RUN_IN_PROGRESS_STATUSES exactly (reducing
    /// is deliberately excluded).
    public var isRunInProgress: Bool {
        switch self {
        case .queued, .sharding, .running, .judging: return true
        default: return false
        }
    }

    public var isTerminal: Bool { self == .done || self == .failed }
}

/// One shard of a run: an E2B sandbox mining a project scope. `scope` on the
/// wire is `{project_id}` for a single project, `{project_ids}` for a bucket
/// of tiny projects, or null for the owner-wide `__all__` shard.
public struct SkillRunShard: Equatable, Sendable {
    public let shardKey: String
    public let scopeKind: String?
    public let projectIDs: [String]
    public let status: String
    public let sandboxId: String?
    public let sessionCount: Int
    public let spanCount: Int
    public let startedAt: String?
    public let endedAt: String?
    public let error: String?

    public var phase: SkillRunPhase { SkillRunPhase(status: status) }
    public var startedAtDate: Date? { CloudDate.parse(startedAt) }
    public var endedAtDate: Date? { CloudDate.parse(endedAt) }

    /// Scope label matching the web activity panel: the project id for a
    /// single-project shard, "{n} projects" for buckets, else the shard key.
    public var scopeLabel: String {
        if projectIDs.count == 1 { return projectIDs[0] }
        if projectIDs.count > 1 { return "\(projectIDs.count) projects" }
        return shardKey
    }

    /// Same duration rule as the run: non-terminal shards (reducing included)
    /// tick against now, terminal ones freeze at endedAt.
    public func duration(now: Date = Date()) -> TimeInterval? {
        guard let start = startedAtDate else { return nil }
        if phase.isTerminal {
            guard let end = endedAtDate else { return nil }
            return max(0, end.timeIntervalSince(start))
        }
        return max(0, now.timeIntervalSince(start))
    }

    public init(shardKey: String, scopeKind: String? = nil, projectIDs: [String] = [],
                status: String = "", sandboxId: String? = nil, sessionCount: Int = 0,
                spanCount: Int = 0, startedAt: String? = nil, endedAt: String? = nil,
                error: String? = nil) {
        self.shardKey = shardKey
        self.scopeKind = scopeKind
        self.projectIDs = projectIDs
        self.status = status
        self.sandboxId = sandboxId
        self.sessionCount = sessionCount
        self.spanCount = spanCount
        self.startedAt = startedAt
        self.endedAt = endedAt
        self.error = error
    }
}

extension SkillRunShard: Decodable {
    private enum CodingKeys: String, CodingKey {
        case shardKey, scopeKind, scope, status, sandboxId
        case sessionCount, spanCount, startedAt, endedAt, error
    }

    private struct Scope: Decodable {
        let projectId: String?
        let projectIds: [String]?

        enum CodingKeys: String, CodingKey {
            case projectId = "project_id"
            case projectIds = "project_ids"
        }
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        shardKey = try c.decodeIfPresent(String.self, forKey: .shardKey) ?? ""
        scopeKind = try? c.decodeIfPresent(String.self, forKey: .scopeKind)
        if let scope = try? c.decodeIfPresent(Scope.self, forKey: .scope) {
            if let one = scope.projectId, !one.isEmpty {
                projectIDs = [one]
            } else {
                projectIDs = scope.projectIds ?? []
            }
        } else {
            projectIDs = []
        }
        status = try c.decodeIfPresent(String.self, forKey: .status) ?? ""
        sandboxId = try c.decodeIfPresent(String.self, forKey: .sandboxId)
        sessionCount = try c.decodeIfPresent(Int.self, forKey: .sessionCount) ?? 0
        spanCount = try c.decodeIfPresent(Int.self, forKey: .spanCount) ?? 0
        startedAt = try c.decodeIfPresent(String.self, forKey: .startedAt)
        endedAt = try c.decodeIfPresent(String.self, forKey: .endedAt)
        error = try c.decodeIfPresent(String.self, forKey: .error)
    }
}

/// One skill candidate a run produced. Verdicts: confirmed (auto-adopted),
/// needs-edits (goes to Review), refuted and unverified (diagnostics only).
/// `adjudicated` marks a third-model tiebreak.
public struct SkillRunDossier: Equatable, Sendable {
    public let name: String
    public let clusterKey: String?
    public let verdict: String?
    public let confidence: Double?
    public let adjudicated: Bool
    public let hasSkill: Bool

    public init(name: String, clusterKey: String? = nil, verdict: String? = nil,
                confidence: Double? = nil, adjudicated: Bool = false, hasSkill: Bool = false) {
        self.name = name
        self.clusterKey = clusterKey
        self.verdict = verdict
        self.confidence = confidence
        self.adjudicated = adjudicated
        self.hasSkill = hasSkill
    }
}

extension SkillRunDossier: Decodable {
    private enum CodingKeys: String, CodingKey {
        case name, clusterKey, verdict, confidence, adjudicated, hasSkill
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        clusterKey = try? c.decodeIfPresent(String.self, forKey: .clusterKey)
        verdict = try c.decodeIfPresent(String.self, forKey: .verdict)
        if let d = try? c.decodeIfPresent(Double.self, forKey: .confidence) {
            confidence = d
        } else if let i = try? c.decodeIfPresent(Int.self, forKey: .confidence) {
            confidence = Double(i)
        } else {
            confidence = nil
        }
        adjudicated = (try? c.decodeIfPresent(Bool.self, forKey: .adjudicated)) ?? false
        hasSkill = (try? c.decodeIfPresent(Bool.self, forKey: .hasSkill)) ?? false
    }
}

/// One background skills run. GET /api/v1/lore/runs is both list and detail:
/// the last 50 runs, newest first, each carrying its shards, produced
/// dossiers, and verdict tallies. There is no separate run-detail endpoint.
public struct SkillRun: Identifiable, Equatable, Sendable {
    public let id: String
    public let status: String
    /// manual | cron | nudge
    public let trigger: String?
    public let shardCount: Int
    public let shardsDone: Int
    public let sessionCount: Int
    public let spanCount: Int
    public let watermarkFrom: String?
    public let watermarkTo: String?
    public let createdAt: String?
    public let startedAt: String?
    public let endedAt: String?
    public let updatedAt: String?
    public let error: String?
    /// Sum of shard session counts: how many sessions this run mined.
    public let sessionsMined: Int
    public let shards: [SkillRunShard]
    public let dossierVerdicts: [String: Int]
    public let dossierTotal: Int
    public let dossiers: [SkillRunDossier]

    /// Display order for verdict tally chips.
    public static let verdictOrder = ["confirmed", "needs-edits", "refuted", "unverified"]

    public var phase: SkillRunPhase { SkillRunPhase(status: status) }
    public var isInProgress: Bool { phase.isRunInProgress }

    public var createdAtDate: Date? { CloudDate.parse(createdAt) }
    public var startedAtDate: Date? { CloudDate.parse(startedAt) }
    public var endedAtDate: Date? { CloudDate.parse(endedAt) }
    public var updatedAtDate: Date? { CloudDate.parse(updatedAt) }

    /// Duration per the web activity panel rules: in-progress runs tick live
    /// against `now`; terminal runs freeze at endedAt, falling back to
    /// updatedAt because fail paths can leave endedAt null.
    public func duration(now: Date = Date()) -> TimeInterval? {
        guard let start = startedAtDate ?? createdAtDate else { return nil }
        if phase.isTerminal {
            guard let end = endedAtDate ?? updatedAtDate else { return nil }
            return max(0, end.timeIntervalSince(start))
        }
        return max(0, now.timeIntervalSince(start))
    }

    /// Verdict tallies in canonical order (confirmed, needs-edits, refuted,
    /// unverified), then any unknown verdicts alphabetically. Zero counts are
    /// dropped.
    public var verdictTallies: [(verdict: String, count: Int)] {
        var remaining = dossierVerdicts
        var tallies: [(String, Int)] = []
        for verdict in Self.verdictOrder {
            if let count = remaining.removeValue(forKey: verdict), count > 0 {
                tallies.append((verdict, count))
            }
        }
        for verdict in remaining.keys.sorted() {
            if let count = remaining[verdict], count > 0 {
                tallies.append((verdict, count))
            }
        }
        return tallies
    }

    public init(id: String, status: String, trigger: String? = nil, shardCount: Int = 0,
                shardsDone: Int = 0, sessionCount: Int = 0, spanCount: Int = 0,
                watermarkFrom: String? = nil, watermarkTo: String? = nil,
                createdAt: String? = nil, startedAt: String? = nil, endedAt: String? = nil,
                updatedAt: String? = nil, error: String? = nil, sessionsMined: Int = 0,
                shards: [SkillRunShard] = [], dossierVerdicts: [String: Int] = [:],
                dossierTotal: Int = 0, dossiers: [SkillRunDossier] = []) {
        self.id = id
        self.status = status
        self.trigger = trigger
        self.shardCount = shardCount
        self.shardsDone = shardsDone
        self.sessionCount = sessionCount
        self.spanCount = spanCount
        self.watermarkFrom = watermarkFrom
        self.watermarkTo = watermarkTo
        self.createdAt = createdAt
        self.startedAt = startedAt
        self.endedAt = endedAt
        self.updatedAt = updatedAt
        self.error = error
        self.sessionsMined = sessionsMined
        self.shards = shards
        self.dossierVerdicts = dossierVerdicts
        self.dossierTotal = dossierTotal
        self.dossiers = dossiers
    }
}

extension SkillRun: Decodable {
    private enum CodingKeys: String, CodingKey {
        case id, status, trigger, shardCount, shardsDone, sessionCount, spanCount
        case watermarkFrom, watermarkTo, createdAt, startedAt, endedAt, updatedAt
        case error, sessionsMined, shards, dossierVerdicts, dossierTotal, dossiers
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        status = try c.decodeIfPresent(String.self, forKey: .status) ?? ""
        trigger = try c.decodeIfPresent(String.self, forKey: .trigger)
        shardCount = try c.decodeIfPresent(Int.self, forKey: .shardCount) ?? 0
        shardsDone = try c.decodeIfPresent(Int.self, forKey: .shardsDone) ?? 0
        sessionCount = try c.decodeIfPresent(Int.self, forKey: .sessionCount) ?? 0
        spanCount = try c.decodeIfPresent(Int.self, forKey: .spanCount) ?? 0
        watermarkFrom = try c.decodeIfPresent(String.self, forKey: .watermarkFrom)
        watermarkTo = try c.decodeIfPresent(String.self, forKey: .watermarkTo)
        createdAt = try c.decodeIfPresent(String.self, forKey: .createdAt)
        startedAt = try c.decodeIfPresent(String.self, forKey: .startedAt)
        endedAt = try c.decodeIfPresent(String.self, forKey: .endedAt)
        updatedAt = try c.decodeIfPresent(String.self, forKey: .updatedAt)
        error = try c.decodeIfPresent(String.self, forKey: .error)
        sessionsMined = try c.decodeIfPresent(Int.self, forKey: .sessionsMined) ?? 0
        shards = (try? c.decodeIfPresent([SkillRunShard].self, forKey: .shards)) ?? []
        if let counts = try? c.decodeIfPresent([String: Int].self, forKey: .dossierVerdicts) {
            dossierVerdicts = counts
        } else if let doubles = try? c.decodeIfPresent([String: Double].self, forKey: .dossierVerdicts) {
            dossierVerdicts = doubles.mapValues { Int($0) }
        } else {
            dossierVerdicts = [:]
        }
        dossierTotal = try c.decodeIfPresent(Int.self, forKey: .dossierTotal) ?? 0
        dossiers = (try? c.decodeIfPresent([SkillRunDossier].self, forKey: .dossiers)) ?? []
    }
}

// MARK: - Review queue (GET/PATCH /api/v1/lore/dossiers)

/// PATCH action on a dossier: approve forges the skill (Keep), decline
/// dismisses it (Dismiss). There is no skill delete endpoint anywhere;
/// decline is the only removal the surface offers.
public enum SkillDossierAction: String, Sendable {
    case approve
    case decline
}

/// One raw dossier from GET /api/v1/lore/dossiers?status=pending: the
/// borderline needs-edits candidates waiting for Keep or Dismiss.
public struct SkillDossier: Identifiable, Equatable, Sendable {
    public let id: String
    public let name: String?
    public let clusterKey: String?
    public let status: String?
    public let verdict: String?
    public let confidence: Double?
    public let skillMd: String?
    public let createdAt: String?

    public var createdAtDate: Date? { CloudDate.parse(createdAt) }
    /// Review rows fall back to the cluster key when unnamed, matching the
    /// web library merge.
    public var displayName: String {
        if let name, !name.isEmpty { return name }
        return clusterKey ?? id
    }

    public init(id: String, name: String? = nil, clusterKey: String? = nil,
                status: String? = nil, verdict: String? = nil, confidence: Double? = nil,
                skillMd: String? = nil, createdAt: String? = nil) {
        self.id = id
        self.name = name
        self.clusterKey = clusterKey
        self.status = status
        self.verdict = verdict
        self.confidence = confidence
        self.skillMd = skillMd
        self.createdAt = createdAt
    }
}

extension SkillDossier: Decodable {
    private enum CodingKeys: String, CodingKey {
        case id, name, clusterKey, status, verdict, confidence, skillMd, createdAt
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        name = try c.decodeIfPresent(String.self, forKey: .name)
        clusterKey = try? c.decodeIfPresent(String.self, forKey: .clusterKey)
        status = try c.decodeIfPresent(String.self, forKey: .status)
        verdict = try c.decodeIfPresent(String.self, forKey: .verdict)
        if let d = try? c.decodeIfPresent(Double.self, forKey: .confidence) {
            confidence = d
        } else if let i = try? c.decodeIfPresent(Int.self, forKey: .confidence) {
            confidence = Double(i)
        } else {
            confidence = nil
        }
        skillMd = try c.decodeIfPresent(String.self, forKey: .skillMd)
        createdAt = try c.decodeIfPresent(String.self, forKey: .createdAt)
    }
}

// MARK: - Forged skills down-channel (GET /api/v1/lore/skills)

/// One forged skill row from GET /api/v1/lore/skills?install_state=...: the
/// install-state channel the CLI uses, with the drift recommendation badge.
/// This is where installed-versus-available state lives server-side.
public struct ForgedSkill: Identifiable, Equatable, Sendable {
    public let name: String
    public let status: String?
    public let skillMd: String?
    public let clusterKey: String?
    public let fingerprint: String?
    public let contentSha: String?
    /// available | installed
    public let installState: String?
    /// Drift ladder: up-to-date, minor-drift, update, update-carefully,
    /// suppress, re-engage, orphaned.
    public let recommendation: String?

    public var id: String { name }
    public var isInstalled: Bool { installState == "installed" }

    public init(name: String, status: String? = nil, skillMd: String? = nil,
                clusterKey: String? = nil, fingerprint: String? = nil,
                contentSha: String? = nil, installState: String? = nil,
                recommendation: String? = nil) {
        self.name = name
        self.status = status
        self.skillMd = skillMd
        self.clusterKey = clusterKey
        self.fingerprint = fingerprint
        self.contentSha = contentSha
        self.installState = installState
        self.recommendation = recommendation
    }
}

extension ForgedSkill: Decodable {
    private enum CodingKeys: String, CodingKey {
        case name, status, skillMd, clusterKey, fingerprint, contentSha
        case installState, recommendation
    }

    private struct RecommendationBox: Decodable {
        let recommendation: String?
        let kind: String?
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        status = try c.decodeIfPresent(String.self, forKey: .status)
        skillMd = try c.decodeIfPresent(String.self, forKey: .skillMd)
        clusterKey = try? c.decodeIfPresent(String.self, forKey: .clusterKey)
        fingerprint = try? c.decodeIfPresent(String.self, forKey: .fingerprint)
        contentSha = try? c.decodeIfPresent(String.self, forKey: .contentSha)
        installState = try c.decodeIfPresent(String.self, forKey: .installState)
        // The recommendation may be a bare string or a structured badge.
        if let flat = try? c.decodeIfPresent(String.self, forKey: .recommendation) {
            recommendation = flat
        } else if let boxed = try? c.decodeIfPresent(RecommendationBox.self, forKey: .recommendation) {
            recommendation = boxed.recommendation ?? boxed.kind
        } else {
            recommendation = nil
        }
    }
}

// MARK: - Resumable discovery (GET /projects?resumable=true)

/// Session metadata summary inlined under ?resumable=true. Not the blob:
/// listing rows only, per the CLI resume client shape.
public struct ResumableSessionSummary: Decodable, Equatable, Sendable {
    public let id: String
    public let clientId: String
    public let projectId: String
    public let projectName: String?
    public let name: String
    public let userTitle: String?
    public let markdownSize: Int?
    public let sessionDataSize: Int?
    public let createdAt: String
    public let updatedAt: String
    public let startedAt: String?
    public let endedAt: String?
    public let metadata: CloudSessionMetadata
    public let etag: String?

    public var createdAtDate: Date? { CloudDate.parse(createdAt) }
    public var updatedAtDate: Date? { CloudDate.parse(updatedAt) }

    /// Title precedence shared with the web UI: userTitle > metadata.title > name.
    public var displayTitle: String {
        if let userTitle, !userTitle.isEmpty { return userTitle }
        if let title = metadata.title, !title.isEmpty { return title }
        return name
    }

    private enum CodingKeys: String, CodingKey {
        case id, clientId, projectId, projectName, name, userTitle
        case markdownSize, sessionDataSize, createdAt, updatedAt
        case startedAt, endedAt, metadata, etag
    }

    public init(id: String, clientId: String, projectId: String, projectName: String? = nil,
                name: String = "", userTitle: String? = nil, markdownSize: Int? = nil,
                sessionDataSize: Int? = nil, createdAt: String = "", updatedAt: String = "",
                startedAt: String? = nil, endedAt: String? = nil,
                metadata: CloudSessionMetadata = CloudSessionMetadata(), etag: String? = nil) {
        self.id = id
        self.clientId = clientId
        self.projectId = projectId
        self.projectName = projectName
        self.name = name
        self.userTitle = userTitle
        self.markdownSize = markdownSize
        self.sessionDataSize = sessionDataSize
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.startedAt = startedAt
        self.endedAt = endedAt
        self.metadata = metadata
        self.etag = etag
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        clientId = try c.decodeIfPresent(String.self, forKey: .clientId) ?? ""
        projectId = try c.decodeIfPresent(String.self, forKey: .projectId) ?? ""
        projectName = try c.decodeIfPresent(String.self, forKey: .projectName)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        userTitle = try c.decodeIfPresent(String.self, forKey: .userTitle)
        markdownSize = try c.decodeIfPresent(Int.self, forKey: .markdownSize)
        sessionDataSize = try c.decodeIfPresent(Int.self, forKey: .sessionDataSize)
        createdAt = try c.decodeIfPresent(String.self, forKey: .createdAt) ?? ""
        updatedAt = try c.decodeIfPresent(String.self, forKey: .updatedAt) ?? ""
        startedAt = try c.decodeIfPresent(String.self, forKey: .startedAt)
        endedAt = try c.decodeIfPresent(String.self, forKey: .endedAt)
        metadata = (try? c.decodeIfPresent(CloudSessionMetadata.self, forKey: .metadata)) ?? CloudSessionMetadata()
        etag = try c.decodeIfPresent(String.self, forKey: .etag)
    }
}

/// One project from GET /projects?resumable=true: metadata plus its resumable
/// session summaries inlined. Projects without resumable sessions are dropped
/// server-side.
public struct ResumableProject: Decodable, Equatable, Sendable {
    public let id: String
    public let name: String
    public let icon: String?
    public let color: String?
    public let lastUpdated: String?
    public let sessionCount: Int?
    public let sessions: [ResumableSessionSummary]

    public var lastUpdatedDate: Date? { CloudDate.parse(lastUpdated) }

    private enum CodingKeys: String, CodingKey {
        case id, name, icon, color, lastUpdated, sessionCount, sessions
    }

    public init(id: String, name: String, icon: String? = nil, color: String? = nil,
                lastUpdated: String? = nil, sessionCount: Int? = nil,
                sessions: [ResumableSessionSummary] = []) {
        self.id = id
        self.name = name
        self.icon = icon
        self.color = color
        self.lastUpdated = lastUpdated
        self.sessionCount = sessionCount
        self.sessions = sessions
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        icon = try c.decodeIfPresent(String.self, forKey: .icon)
        color = try c.decodeIfPresent(String.self, forKey: .color)
        lastUpdated = try c.decodeIfPresent(String.self, forKey: .lastUpdated)
        sessionCount = try c.decodeIfPresent(Int.self, forKey: .sessionCount)
        sessions = (try? c.decodeIfPresent([ResumableSessionSummary].self, forKey: .sessions)) ?? []
    }
}

// MARK: - Resume search snippets

/// A search snippet whose highlight markers (STX 0x02 opens, ETX 0x03 closes,
/// matching the CLI's local search) have been stripped into spans. `text` is
/// the display string; `highlightRanges` are character-offset ranges into it.
public struct HighlightedSnippet: Equatable, Sendable {
    public struct Span: Equatable, Sendable {
        public let text: String
        public let isHighlighted: Bool

        public init(text: String, isHighlighted: Bool) {
            self.text = text
            self.isHighlighted = isHighlighted
        }
    }

    public let spans: [Span]

    public init(spans: [Span]) {
        self.spans = spans
    }

    public var text: String { spans.map(\.text).joined() }

    public var highlightRanges: [Range<Int>] {
        var ranges: [Range<Int>] = []
        var offset = 0
        for span in spans {
            let length = span.text.count
            if span.isHighlighted, length > 0 {
                ranges.append(offset..<(offset + length))
            }
            offset += length
        }
        return ranges
    }

    /// Tolerant of unbalanced markers: a dangling STX highlights to the end,
    /// a stray ETX is ignored.
    public static func parse(_ raw: String) -> HighlightedSnippet {
        let stx: Character = "\u{02}"
        let etx: Character = "\u{03}"
        var spans: [Span] = []
        var current = ""
        var highlighted = false

        func flush() {
            if !current.isEmpty {
                spans.append(Span(text: current, isHighlighted: highlighted))
            }
            current = ""
        }

        for character in raw {
            if character == stx {
                flush()
                highlighted = true
            } else if character == etx {
                flush()
                highlighted = false
            } else {
                current.append(character)
            }
        }
        flush()
        return HighlightedSnippet(spans: spans)
    }
}

/// One hit from POST /api/v1/search/resume.
public struct ResumeSearchHit: Decodable, Equatable, Sendable {
    public let id: String
    public let clientId: String
    public let projectId: String
    public let projectName: String?
    public let name: String
    public let userTitle: String?
    public let createdAt: String
    public let updatedAt: String
    public let startedAt: String?
    public let endedAt: String?
    public let sessionDataSize: Int?
    public let metadata: CloudSessionMetadata
    public let snippet: HighlightedSnippet

    public var updatedAtDate: Date? { CloudDate.parse(updatedAt) }
    public var createdAtDate: Date? { CloudDate.parse(createdAt) }

    public var displayTitle: String {
        if let userTitle, !userTitle.isEmpty { return userTitle }
        if let title = metadata.title, !title.isEmpty { return title }
        return name
    }

    private enum CodingKeys: String, CodingKey {
        case id, clientId, projectId, projectName, name, userTitle
        case createdAt, updatedAt, startedAt, endedAt, sessionDataSize
        case metadata, snippet
    }

    public init(id: String, clientId: String, projectId: String, projectName: String? = nil,
                name: String = "", userTitle: String? = nil, createdAt: String = "",
                updatedAt: String = "", startedAt: String? = nil, endedAt: String? = nil,
                sessionDataSize: Int? = nil, metadata: CloudSessionMetadata = CloudSessionMetadata(),
                snippet: HighlightedSnippet = HighlightedSnippet(spans: [])) {
        self.id = id
        self.clientId = clientId
        self.projectId = projectId
        self.projectName = projectName
        self.name = name
        self.userTitle = userTitle
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.startedAt = startedAt
        self.endedAt = endedAt
        self.sessionDataSize = sessionDataSize
        self.metadata = metadata
        self.snippet = snippet
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        clientId = try c.decodeIfPresent(String.self, forKey: .clientId) ?? ""
        projectId = try c.decodeIfPresent(String.self, forKey: .projectId) ?? ""
        projectName = try c.decodeIfPresent(String.self, forKey: .projectName)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        userTitle = try c.decodeIfPresent(String.self, forKey: .userTitle)
        createdAt = try c.decodeIfPresent(String.self, forKey: .createdAt) ?? ""
        updatedAt = try c.decodeIfPresent(String.self, forKey: .updatedAt) ?? ""
        startedAt = try c.decodeIfPresent(String.self, forKey: .startedAt)
        endedAt = try c.decodeIfPresent(String.self, forKey: .endedAt)
        sessionDataSize = try c.decodeIfPresent(Int.self, forKey: .sessionDataSize)
        metadata = (try? c.decodeIfPresent(CloudSessionMetadata.self, forKey: .metadata)) ?? CloudSessionMetadata()
        let raw = try c.decodeIfPresent(String.self, forKey: .snippet) ?? ""
        snippet = HighlightedSnippet.parse(raw)
    }
}

// MARK: - Billing

/// POST /api/v1/billing/checkout answers either {url} (Stripe Checkout) or
/// {alreadySubscribed:true, plan} when the user already has a subscription.
public struct CheckoutResult: Equatable, Sendable {
    public let url: URL?
    public let alreadySubscribed: Bool
    public let plan: String?

    public init(url: URL? = nil, alreadySubscribed: Bool = false, plan: String? = nil) {
        self.url = url
        self.alreadySubscribed = alreadySubscribed
        self.plan = plan
    }
}

extension CheckoutResult: Decodable {
    private enum CodingKeys: String, CodingKey { case url, alreadySubscribed, plan }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let rawURL = try c.decodeIfPresent(String.self, forKey: .url)
        url = rawURL.flatMap(URL.init(string:))
        alreadySubscribed = (try? c.decodeIfPresent(Bool.self, forKey: .alreadySubscribed)) ?? false
        plan = try c.decodeIfPresent(String.self, forKey: .plan)
    }
}
