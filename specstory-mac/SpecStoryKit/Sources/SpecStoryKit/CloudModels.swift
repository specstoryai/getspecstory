import Foundation

// MARK: - Envelope

/// SpecStory Cloud wraps every REST response in {"success":true,"data":...}.
/// Markdown and GraphQL responses are the exceptions and are handled separately.
struct APIEnvelope<T: Decodable>: Decodable {
    let success: Bool
    let data: T?
    let error: String?
}

// MARK: - Dates

/// Cloud timestamps are ISO 8601, sometimes with fractional seconds. Models
/// keep the raw strings and expose computed Date accessors through this parser.
public enum CloudDate {
    private static let fractional: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()
    private static let plain = ISO8601DateFormatter()

    public static func parse(_ string: String?) -> Date? {
        guard let string, !string.isEmpty else { return nil }
        return fractional.date(from: string) ?? plain.date(from: string)
    }
}

// MARK: - Device metadata

public struct DeviceMetadata: Codable, Sendable {
    public let hostname: String
    public let os: String
    public let osVersion: String
    public let osDisplayName: String
    public let architecture: String
    public let username: String
    public let client: String
    public let clientVersion: String

    enum CodingKeys: String, CodingKey {
        case hostname
        case os
        case osVersion = "os_version"
        case osDisplayName = "os_display_name"
        case architecture
        case username
        case client
        case clientVersion = "client_version"
    }

    public init(hostname: String, os: String, osVersion: String, osDisplayName: String,
                architecture: String, username: String, client: String, clientVersion: String) {
        self.hostname = hostname
        self.os = os
        self.osVersion = osVersion
        self.osDisplayName = osDisplayName
        self.architecture = architecture
        self.username = username
        self.client = client
        self.clientVersion = clientVersion
    }

    public static func current(clientVersion: String) -> DeviceMetadata {
        let version = ProcessInfo.processInfo.operatingSystemVersion
        var uts = utsname()
        uname(&uts)
        let machine = withUnsafeBytes(of: &uts.machine) { raw in
            String(decoding: raw.prefix(while: { $0 != 0 }), as: UTF8.self)
        }
        return DeviceMetadata(
            hostname: ProcessInfo.processInfo.hostName,
            os: "darwin",
            osVersion: "\(version.majorVersion).\(version.minorVersion).\(version.patchVersion)",
            osDisplayName: "macOS",
            architecture: machine,
            username: NSUserName(),
            client: "specstory-macapp",
            clientVersion: clientVersion
        )
    }
}

// MARK: - Auth results

public struct DeviceLoginResult: Equatable, Sendable {
    public let refreshToken: String
    public let createdAt: String
    public let expiresAt: String
    public let email: String

    public var expiresAtDate: Date? { CloudDate.parse(expiresAt) }

    public init(refreshToken: String, createdAt: String, expiresAt: String, email: String) {
        self.refreshToken = refreshToken
        self.createdAt = createdAt
        self.expiresAt = expiresAt
        self.email = email
    }
}

extension DeviceLoginResult: Decodable {
    private enum CodingKeys: String, CodingKey { case refreshToken, createdAt, expiresAt, user }
    private struct User: Decodable { let email: String? }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        refreshToken = try c.decode(String.self, forKey: .refreshToken)
        createdAt = try c.decodeIfPresent(String.self, forKey: .createdAt) ?? ""
        expiresAt = try c.decodeIfPresent(String.self, forKey: .expiresAt) ?? ""
        email = (try c.decodeIfPresent(User.self, forKey: .user))?.email ?? ""
    }
}

public struct AccessTokenResult: Decodable, Equatable, Sendable {
    public let accessToken: String
    public let createdAt: String?
    public let expiresAt: String

    public var expiresAtDate: Date? { CloudDate.parse(expiresAt) }

    public init(accessToken: String, createdAt: String?, expiresAt: String) {
        self.accessToken = accessToken
        self.createdAt = createdAt
        self.expiresAt = expiresAt
    }
}

// MARK: - Sessions

public struct CloudSessionMetadata: Codable, Equatable, Sendable {
    public let agentName: String?
    public let deviceId: String?
    public let machineName: String?
    public let title: String?
    public let gitBranches: [String]?
    public let llmModels: [String]?

    public init(agentName: String? = nil, deviceId: String? = nil, machineName: String? = nil,
                title: String? = nil, gitBranches: [String]? = nil, llmModels: [String]? = nil) {
        self.agentName = agentName
        self.deviceId = deviceId
        self.machineName = machineName
        self.title = title
        self.gitBranches = gitBranches
        self.llmModels = llmModels
    }
}

/// One session as returned by /sessions/recent and project session lists.
/// `id` is the server UUID; URL paths always use `clientId` + `projectId`
/// (client identifiers), never server UUIDs. Decoding is tolerant: unknown
/// fields are ignored and timestamps stay ISO 8601 strings.
public struct CloudSession: Decodable, Equatable, Sendable {
    public let id: String
    public let clientId: String
    public let projectId: String
    public let name: String
    public let userTitle: String?
    public let createdAt: String
    public let updatedAt: String
    public let startedAt: String?
    public let endedAt: String?
    public let sessionDataSize: Int?
    public let metadata: CloudSessionMetadata
    public let etag: String?

    public var createdAtDate: Date? { CloudDate.parse(createdAt) }
    public var updatedAtDate: Date? { CloudDate.parse(updatedAt) }
    public var startedAtDate: Date? { CloudDate.parse(startedAt) }
    public var endedAtDate: Date? { CloudDate.parse(endedAt) }

    /// Title precedence shared with the web UI: userTitle > metadata.title > name.
    public var displayTitle: String {
        if let userTitle, !userTitle.isEmpty { return userTitle }
        if let title = metadata.title, !title.isEmpty { return title }
        return name
    }

    private enum CodingKeys: String, CodingKey {
        case id, clientId, projectId, name, userTitle, createdAt, updatedAt
        case startedAt, endedAt, sessionDataSize, metadata, etag
    }

    public init(id: String, clientId: String, projectId: String, name: String,
                userTitle: String? = nil, createdAt: String = "", updatedAt: String = "",
                startedAt: String? = nil, endedAt: String? = nil, sessionDataSize: Int? = nil,
                metadata: CloudSessionMetadata = CloudSessionMetadata(), etag: String? = nil) {
        self.id = id
        self.clientId = clientId
        self.projectId = projectId
        self.name = name
        self.userTitle = userTitle
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.startedAt = startedAt
        self.endedAt = endedAt
        self.sessionDataSize = sessionDataSize
        self.metadata = metadata
        self.etag = etag
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        clientId = try c.decodeIfPresent(String.self, forKey: .clientId) ?? ""
        projectId = try c.decodeIfPresent(String.self, forKey: .projectId) ?? ""
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        userTitle = try c.decodeIfPresent(String.self, forKey: .userTitle)
        createdAt = try c.decodeIfPresent(String.self, forKey: .createdAt) ?? ""
        updatedAt = try c.decodeIfPresent(String.self, forKey: .updatedAt) ?? ""
        startedAt = try c.decodeIfPresent(String.self, forKey: .startedAt)
        endedAt = try c.decodeIfPresent(String.self, forKey: .endedAt)
        sessionDataSize = try c.decodeIfPresent(Int.self, forKey: .sessionDataSize)
        metadata = (try? c.decodeIfPresent(CloudSessionMetadata.self, forKey: .metadata)) ?? CloudSessionMetadata()
        etag = try c.decodeIfPresent(String.self, forKey: .etag)
    }
}

// MARK: - Projects

public struct CloudProject: Decodable, Equatable, Sendable {
    public let id: String
    public let name: String
    public let icon: String?
    public let color: String?
    public let sessionCount: Int?
    public let lastUpdated: String?
    public let totalMarkdownSize: Int?
    public let createdAt: String?
    public let updatedAt: String?

    public var lastUpdatedDate: Date? { CloudDate.parse(lastUpdated) }

    private enum CodingKeys: String, CodingKey {
        case id, name, icon, color, sessionCount, lastUpdated, totalMarkdownSize, createdAt, updatedAt
    }

    public init(id: String, name: String, icon: String? = nil, color: String? = nil,
                sessionCount: Int? = nil, lastUpdated: String? = nil, totalMarkdownSize: Int? = nil,
                createdAt: String? = nil, updatedAt: String? = nil) {
        self.id = id
        self.name = name
        self.icon = icon
        self.color = color
        self.sessionCount = sessionCount
        self.lastUpdated = lastUpdated
        self.totalMarkdownSize = totalMarkdownSize
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        icon = try c.decodeIfPresent(String.self, forKey: .icon)
        color = try c.decodeIfPresent(String.self, forKey: .color)
        sessionCount = try c.decodeIfPresent(Int.self, forKey: .sessionCount)
        lastUpdated = try c.decodeIfPresent(String.self, forKey: .lastUpdated)
        totalMarkdownSize = try c.decodeIfPresent(Int.self, forKey: .totalMarkdownSize)
        createdAt = try c.decodeIfPresent(String.self, forKey: .createdAt)
        updatedAt = try c.decodeIfPresent(String.self, forKey: .updatedAt)
    }
}

// MARK: - Markdown + HEAD

public enum SessionMarkdown: Equatable, Sendable {
    case notModified
    case content(markdown: String, etag: String?)
}

/// Metadata-only view of a session from a HEAD request.
/// sessionDataSize of 0 means not cloud-resumable (legacy upload).
public struct SessionHead: Equatable, Sendable {
    public let etag: String?
    public let markdownSize: Int
    public let rawDataSize: Int
    public let sessionDataSize: Int
    public let lastModified: String?

    public init(etag: String?, markdownSize: Int, rawDataSize: Int, sessionDataSize: Int, lastModified: String? = nil) {
        self.etag = etag
        self.markdownSize = markdownSize
        self.rawDataSize = rawDataSize
        self.sessionDataSize = sessionDataSize
        self.lastModified = lastModified
    }
}

// MARK: - Entitlement / flags / tools

/// Billing entitlement. Feature checks fail closed: anything the server did
/// not explicitly enable reads as false.
public struct Entitlement: Equatable, Sendable {
    public let plan: String?
    public let features: [String: Bool]

    public init(plan: String?, features: [String: Bool]) {
        self.plan = plan
        self.features = features
    }

    public func isEnabled(_ feature: String) -> Bool {
        features[feature] ?? false
    }
}

extension Entitlement: Decodable {
    private struct AnyKey: CodingKey {
        let stringValue: String
        var intValue: Int? { nil }
        init(_ string: String) { stringValue = string }
        init?(stringValue: String) { self.stringValue = stringValue }
        init?(intValue: Int) { nil }
    }

    public init(from decoder: Decoder) throws {
        var container = try decoder.container(keyedBy: AnyKey.self)
        // Tolerate both {plan,features} and {entitlement:{plan,features}} shapes.
        if let nested = try? container.nestedContainer(keyedBy: AnyKey.self, forKey: AnyKey("entitlement")) {
            container = nested
        }
        plan = try? container.decodeIfPresent(String.self, forKey: AnyKey("plan"))
        var collected: [String: Bool] = [:]
        if let featureContainer = try? container.nestedContainer(keyedBy: AnyKey.self, forKey: AnyKey("features")) {
            for key in featureContainer.allKeys {
                // Skip non-boolean feature values rather than failing the decode.
                if let flag = try? featureContainer.decode(Bool.self, forKey: key) {
                    collected[key.stringValue] = flag
                }
            }
        }
        features = collected
    }
}

public struct UserTool: Decodable, Equatable, Sendable {
    public let agentName: String
    public let sessionCount: Int
    public let lastUsed: String?

    public var lastUsedDate: Date? { CloudDate.parse(lastUsed) }

    private enum CodingKeys: String, CodingKey { case agentName, sessionCount, lastUsed }

    public init(agentName: String, sessionCount: Int, lastUsed: String? = nil) {
        self.agentName = agentName
        self.sessionCount = sessionCount
        self.lastUsed = lastUsed
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        agentName = try c.decodeIfPresent(String.self, forKey: .agentName) ?? ""
        sessionCount = try c.decodeIfPresent(Int.self, forKey: .sessionCount) ?? 0
        lastUsed = try c.decodeIfPresent(String.self, forKey: .lastUsed)
    }
}

// MARK: - Search

public struct SearchProjectRef: Decodable, Equatable, Sendable {
    public let id: String?
    public let name: String?
    public let icon: String?
    public let color: String?

    public init(id: String? = nil, name: String? = nil, icon: String? = nil, color: String? = nil) {
        self.id = id
        self.name = name
        self.icon = icon
        self.color = color
    }
}

public struct SearchExchange: Decodable, Equatable, Sendable {
    public let id: String?
    public let content: String?
    public let orderNumber: Int?

    public init(id: String? = nil, content: String? = nil, orderNumber: Int? = nil) {
        self.id = id
        self.content = content
        self.orderNumber = orderNumber
    }
}

/// One result from the GraphQL searchSessions query.
public struct SearchHit: Decodable, Equatable, Sendable {
    public let id: String
    public let clientId: String?
    public let projectId: String?
    public let name: String?
    public let userTitle: String?
    public let rank: Double?
    public let matchingExchanges: [SearchExchange]?
    public let project: SearchProjectRef?

    private enum CodingKeys: String, CodingKey {
        case id, clientId, sessionId, projectId, name, userTitle, rank, matchingExchanges, project
    }

    public init(id: String, clientId: String? = nil, projectId: String? = nil, name: String? = nil,
                userTitle: String? = nil, rank: Double? = nil,
                matchingExchanges: [SearchExchange]? = nil, project: SearchProjectRef? = nil) {
        self.id = id
        self.clientId = clientId
        self.projectId = projectId
        self.name = name
        self.userTitle = userTitle
        self.rank = rank
        self.matchingExchanges = matchingExchanges
        self.project = project
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        // The GraphQL result exposes the session client id as `sessionId`
        // (there is no clientId field on SearchResultItem).
        clientId = try c.decodeIfPresent(String.self, forKey: .clientId)
            ?? c.decodeIfPresent(String.self, forKey: .sessionId)
        name = try c.decodeIfPresent(String.self, forKey: .name)
        userTitle = try c.decodeIfPresent(String.self, forKey: .userTitle)
        // rank can arrive as Float or Int depending on the SQL path.
        if let d = try? c.decodeIfPresent(Double.self, forKey: .rank) {
            rank = d
        } else if let i = try? c.decodeIfPresent(Int.self, forKey: .rank) {
            rank = Double(i)
        } else {
            rank = nil
        }
        matchingExchanges = try? c.decodeIfPresent([SearchExchange].self, forKey: .matchingExchanges)
        let projectRef = try? c.decodeIfPresent(SearchProjectRef.self, forKey: .project)
        project = projectRef
        // Prefer the project ref's client id (workspaces.project_id); the
        // top-level projectId is the server-side row id.
        let topLevelProjectID = try c.decodeIfPresent(String.self, forKey: .projectId)
        projectId = projectRef?.id ?? topLevelProjectID
    }
}
