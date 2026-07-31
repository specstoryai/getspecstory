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

    public var id: String { name }
    public var updatedAtDate: Date? { CloudDate.parse(updatedAt) }

    public init(name: String, description: String? = nil, content: String = "",
                state: String? = nil, updatedAt: String? = nil, installedAgents: [String]? = nil,
                confidence: Double? = nil, verdict: String? = nil,
                recommendation: String? = nil, dossierId: String? = nil) {
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
    }
}

extension SkillRow: Decodable {
    private enum CodingKeys: String, CodingKey {
        case name, description, trigger, content, skillMd, state
        case updatedAt, createdAt, installedAgents, confidence, verdict
        case recommendation, dossierId
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
