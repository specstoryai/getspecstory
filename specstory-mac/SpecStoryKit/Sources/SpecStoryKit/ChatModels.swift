import Foundation

/// One retrieved source behind a RAG answer.
///
/// Emitted inline by the stream ("chat.stream.embedding_search_results",
/// key "exchanges") and persisted on chat history messages ("message_chunks").
/// Field names on the wire are snake_case. History rows carry the citable
/// chunk id as "exchange_chunk_id" rather than "chunk_id", so decoding falls
/// back accordingly; either way `chunkID` resolves via GET /chunks/:id.
public struct ChatSource: Codable, Equatable, Sendable {
    public let chunkID: String
    public let exchangeID: String
    public let exchangeChunkID: String?
    public let sessionID: String
    public let sessionClientID: String
    public let projectID: String
    public let sessionName: String
    public let userTitle: String?
    public let sessionSummary: String?
    public let projectName: String?
    public let projectIcon: String?
    public let projectColor: String?

    public init(
        chunkID: String,
        exchangeID: String,
        exchangeChunkID: String? = nil,
        sessionID: String,
        sessionClientID: String,
        projectID: String,
        sessionName: String,
        userTitle: String? = nil,
        sessionSummary: String? = nil,
        projectName: String? = nil,
        projectIcon: String? = nil,
        projectColor: String? = nil
    ) {
        self.chunkID = chunkID
        self.exchangeID = exchangeID
        self.exchangeChunkID = exchangeChunkID
        self.sessionID = sessionID
        self.sessionClientID = sessionClientID
        self.projectID = projectID
        self.sessionName = sessionName
        self.userTitle = userTitle
        self.sessionSummary = sessionSummary
        self.projectName = projectName
        self.projectIcon = projectIcon
        self.projectColor = projectColor
    }

    enum CodingKeys: String, CodingKey {
        case chunkID = "chunk_id"
        case exchangeID = "exchange_id"
        case exchangeChunkID = "exchange_chunk_id"
        case sessionID = "session_id"
        case sessionClientID = "session_client_id"
        case projectID = "project_id"
        case sessionName = "session_name"
        case userTitle = "user_title"
        case sessionSummary = "session_summary"
        case projectName = "project_name"
        case projectIcon = "project_icon"
        case projectColor = "project_color"
    }

    public init(from decoder: Decoder) throws {
        // Tolerant by design: a partially populated source should not sink the
        // whole event, so every field decodes best-effort.
        let container = try decoder.container(keyedBy: CodingKeys.self)
        func string(_ key: CodingKeys) -> String? {
            (try? container.decodeIfPresent(String.self, forKey: key)) ?? nil
        }
        let exchangeChunk = string(.exchangeChunkID)
        chunkID = string(.chunkID) ?? exchangeChunk ?? ""
        exchangeID = string(.exchangeID) ?? ""
        exchangeChunkID = exchangeChunk
        sessionID = string(.sessionID) ?? ""
        sessionClientID = string(.sessionClientID) ?? ""
        projectID = string(.projectID) ?? ""
        sessionName = string(.sessionName) ?? ""
        userTitle = string(.userTitle)
        sessionSummary = string(.sessionSummary)
        projectName = string(.projectName)
        projectIcon = string(.projectIcon)
        projectColor = string(.projectColor)
    }
}

/// One event on the /api/v1/chat NDJSON stream. Answer text accumulation
/// happens in the app layer; `.chunk` carries only the delta, with citation
/// markers like [chunk:CHUNK_ID] left inline for the UI to parse.
public enum ChatStreamEvent: Equatable, Sendable {
    case start(chatSessionID: String)
    case status(String)
    case queryRewritten(String)
    case sources([ChatSource])
    case chunk(String)
    case end
    case failure(String)
}

extension ChatStreamEvent {
    /// Maps one NDJSON line to an event. Returns nil for malformed lines,
    /// unknown event types, and lines missing their load-bearing field; the
    /// stream skips those rather than failing.
    public static func parse(line: String) -> ChatStreamEvent? {
        guard
            let data = line.data(using: .utf8),
            let decoded = try? JSONDecoder().decode(StreamLine.self, from: data),
            let type = decoded.data.type
        else {
            return nil
        }
        let payload = decoded.data
        switch type {
        case "chat.stream.start":
            return .start(chatSessionID: payload.chatSessionID ?? "")
        case "chat.stream.event":
            return .status(payload.bestText ?? "")
        case "chat.stream.query_rewritten":
            // The "response" field is boilerplate ("Query rewritten."); only
            // the rewrittenQuery extra is meaningful.
            guard let rewritten = payload.rewrittenQuery else { return nil }
            return .queryRewritten(rewritten)
        case "chat.stream.embedding_search_results":
            return .sources(payload.exchanges ?? [])
        case "chat.stream.chunk":
            guard let delta = payload.bestText else { return nil }
            return .chunk(delta)
        case "chat.stream.end":
            return .end
        case "chat.stream.error":
            return .failure(payload.bestText ?? "Chat stream failed.")
        default:
            return nil
        }
    }
}

/// Wire shape: {"data":{chatSessionId, type, timestamp, response?, ...extras}}.
/// The server puts human text in "response"; alternates are decoded as
/// fallbacks in case the payload contract drifts.
private struct StreamLine: Decodable {
    let data: StreamPayload
}

private struct StreamPayload: Decodable {
    let chatSessionID: String?
    let type: String?
    let response: String?
    let message: String?
    let content: String?
    let delta: String?
    let text: String?
    let rewrittenQueryCamel: String?
    let rewrittenQuerySnake: String?
    let exchanges: [ChatSource]?

    var bestText: String? {
        response ?? message ?? content ?? delta ?? text
    }

    var rewrittenQuery: String? {
        rewrittenQueryCamel ?? rewrittenQuerySnake
    }

    enum CodingKeys: String, CodingKey {
        case chatSessionID = "chatSessionId"
        case type
        case response
        case message
        case content
        case delta
        case text
        case rewrittenQueryCamel = "rewrittenQuery"
        case rewrittenQuerySnake = "rewritten_query"
        case exchanges
    }
}

/// One row from GET /api/v1/chats. Timestamps stay wire-format strings
/// (Postgres timestamptz text); formatting is a UI concern.
public struct ChatThreadSummary: Codable, Equatable, Sendable, Identifiable {
    public let id: String
    public let title: String?
    public let createdAt: String?
    public let updatedAt: String?
    public let messageCount: Int?

    public init(id: String, title: String? = nil, createdAt: String? = nil, updatedAt: String? = nil, messageCount: Int? = nil) {
        self.id = id
        self.title = title
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.messageCount = messageCount
    }

    enum CodingKeys: String, CodingKey {
        case id
        case title
        case createdAt = "created_at"
        case updatedAt = "updated_at"
        case messageCount = "messages_count"
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        title = try container.decodeIfPresent(String.self, forKey: .title)
        createdAt = try container.decodeIfPresent(String.self, forKey: .createdAt)
        updatedAt = try container.decodeIfPresent(String.self, forKey: .updatedAt)
        // The server serializes the count as a string ("1"); tolerate both.
        if let count = try? container.decodeIfPresent(Int.self, forKey: .messageCount) {
            messageCount = count
        } else if let raw = try? container.decodeIfPresent(String.self, forKey: .messageCount) {
            messageCount = Int(raw)
        } else {
            messageCount = nil
        }
    }
}

/// One persisted turn in a chat thread: user query plus assistant response
/// (with [chunk:ID] citations inline) plus the sources that were cited.
public struct ChatMessage: Codable, Equatable, Sendable {
    public let id: String
    public let query: String?
    public let response: String?
    public let createdAt: String?
    public let sources: [ChatSource]

    public init(id: String, query: String? = nil, response: String? = nil, createdAt: String? = nil, sources: [ChatSource] = []) {
        self.id = id
        self.query = query
        self.response = response
        self.createdAt = createdAt
        self.sources = sources
    }

    enum CodingKeys: String, CodingKey {
        case id
        case query
        case response
        case createdAt = "created_at"
        case sources = "message_chunks"
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        query = try container.decodeIfPresent(String.self, forKey: .query)
        response = try container.decodeIfPresent(String.self, forKey: .response)
        createdAt = try container.decodeIfPresent(String.self, forKey: .createdAt)
        let decodedSources = try? container.decodeIfPresent([ChatSource].self, forKey: .sources)
        sources = decodedSources.flatMap { $0 } ?? []
    }
}

/// Full thread from GET /api/v1/chat/:id.
public struct ChatThread: Codable, Equatable, Sendable {
    public let id: String
    public let title: String?
    public let createdAt: String?
    public let updatedAt: String?
    public let messages: [ChatMessage]

    public init(id: String, title: String? = nil, createdAt: String? = nil, updatedAt: String? = nil, messages: [ChatMessage] = []) {
        self.id = id
        self.title = title
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.messages = messages
    }

    enum CodingKeys: String, CodingKey {
        case id
        case title
        case createdAt = "created_at"
        case updatedAt = "updated_at"
        case messages
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        title = try container.decodeIfPresent(String.self, forKey: .title)
        createdAt = try container.decodeIfPresent(String.self, forKey: .createdAt)
        updatedAt = try container.decodeIfPresent(String.self, forKey: .updatedAt)
        let decodedMessages = try? container.decodeIfPresent([ChatMessage].self, forKey: .messages)
        messages = decodedMessages.flatMap { $0 } ?? []
    }
}

/// Resolved citation from GET /api/v1/chunks/:id. `conversationTitle` and
/// `workspaceName` live under the response's nested "metadata" object.
public struct ChunkDetail: Codable, Equatable, Sendable {
    public let chunkID: String
    public let exchangeID: String?
    public let sessionID: String?
    public let projectID: String?
    public let content: String
    public let relevanceScore: Double?
    public let projectName: String?
    public let projectIcon: String?
    public let projectColor: String?
    public let conversationTitle: String?
    public let workspaceName: String?

    public init(
        chunkID: String,
        exchangeID: String? = nil,
        sessionID: String? = nil,
        projectID: String? = nil,
        content: String,
        relevanceScore: Double? = nil,
        projectName: String? = nil,
        projectIcon: String? = nil,
        projectColor: String? = nil,
        conversationTitle: String? = nil,
        workspaceName: String? = nil
    ) {
        self.chunkID = chunkID
        self.exchangeID = exchangeID
        self.sessionID = sessionID
        self.projectID = projectID
        self.content = content
        self.relevanceScore = relevanceScore
        self.projectName = projectName
        self.projectIcon = projectIcon
        self.projectColor = projectColor
        self.conversationTitle = conversationTitle
        self.workspaceName = workspaceName
    }

    enum CodingKeys: String, CodingKey {
        case chunkID = "chunk_id"
        case exchangeID = "exchange_id"
        case sessionID = "session_id"
        case projectID = "project_id"
        case content
        case relevanceScore = "relevance_score"
        case projectName = "project_name"
        case projectIcon = "project_icon"
        case projectColor = "project_color"
        case conversationTitle = "conversation_title"
        case workspaceName = "workspace_name"
    }

    private enum WireKeys: String, CodingKey {
        case metadata
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        chunkID = try container.decode(String.self, forKey: .chunkID)
        exchangeID = try container.decodeIfPresent(String.self, forKey: .exchangeID)
        sessionID = try container.decodeIfPresent(String.self, forKey: .sessionID)
        projectID = try container.decodeIfPresent(String.self, forKey: .projectID)
        let decodedContent = try? container.decodeIfPresent(String.self, forKey: .content)
        content = decodedContent.flatMap { $0 } ?? ""
        let decodedScore = try? container.decodeIfPresent(Double.self, forKey: .relevanceScore)
        relevanceScore = decodedScore.flatMap { $0 }
        projectName = try container.decodeIfPresent(String.self, forKey: .projectName)
        projectIcon = try container.decodeIfPresent(String.self, forKey: .projectIcon)
        projectColor = try container.decodeIfPresent(String.self, forKey: .projectColor)
        let wire = try decoder.container(keyedBy: WireKeys.self)
        if let metadata = try? wire.nestedContainer(keyedBy: CodingKeys.self, forKey: .metadata) {
            conversationTitle = (try? metadata.decodeIfPresent(String.self, forKey: .conversationTitle)) ?? nil
            workspaceName = (try? metadata.decodeIfPresent(String.self, forKey: .workspaceName)) ?? nil
        } else {
            conversationTitle = nil
            workspaceName = nil
        }
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(chunkID, forKey: .chunkID)
        try container.encodeIfPresent(exchangeID, forKey: .exchangeID)
        try container.encodeIfPresent(sessionID, forKey: .sessionID)
        try container.encodeIfPresent(projectID, forKey: .projectID)
        try container.encode(content, forKey: .content)
        try container.encodeIfPresent(relevanceScore, forKey: .relevanceScore)
        try container.encodeIfPresent(projectName, forKey: .projectName)
        try container.encodeIfPresent(projectIcon, forKey: .projectIcon)
        try container.encodeIfPresent(projectColor, forKey: .projectColor)
        var wire = encoder.container(keyedBy: WireKeys.self)
        var metadata = wire.nestedContainer(keyedBy: CodingKeys.self, forKey: .metadata)
        try metadata.encodeIfPresent(conversationTitle, forKey: .conversationTitle)
        try metadata.encodeIfPresent(workspaceName, forKey: .workspaceName)
    }
}
