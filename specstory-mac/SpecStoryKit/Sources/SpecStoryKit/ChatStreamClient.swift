import Foundation

public enum ChatStreamError: Error, Equatable {
    /// accessTokenProvider returned nil (signed out).
    case signedOut
    case invalidResponse
    case http(status: Int, body: String)
    case decoding(String)
}

/// Client for the cloud RAG chat endpoints under baseURL (the /api/v1 root).
///
/// `ask` streams POST /chat responses, which are NDJSON (Content-Type
/// application/x-ndjson), not SSE: each line is {"data":{chatSessionId, type,
/// timestamp, ...}}. The client only emits deltas and metadata events; answer
/// text accumulation is the app layer's job.
public final class ChatStreamClient {
    private let baseURL: URL
    private let accessTokenProvider: () async throws -> String?
    private let session: URLSession

    public convenience init(baseURL: URL, accessTokenProvider: @escaping () async throws -> String?) {
        self.init(baseURL: baseURL, accessTokenProvider: accessTokenProvider, session: .shared)
    }

    /// Session injection point for URLProtocol-stubbed tests.
    public init(baseURL: URL, accessTokenProvider: @escaping () async throws -> String?, session: URLSession) {
        self.baseURL = baseURL
        self.accessTokenProvider = accessTokenProvider
        self.session = session
    }

    // MARK: - Streaming ask

    /// Streams RAG chat events for one query. Unknown event types and
    /// malformed lines are skipped; server-reported errors surface as
    /// `.failure` events (the stream itself finishes normally). Transport and
    /// HTTP errors throw. The trailing partial line is flushed at stream end
    /// so a final line without a newline is never lost.
    public func ask(
        query: String,
        chatSessionID: String?,
        projectIDs: [String]?,
        timeFilter: String?,
        agentNames: [String]?
    ) -> AsyncThrowingStream<ChatStreamEvent, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    var request = try await self.authorizedRequest(path: "chat")
                    request.httpMethod = "POST"
                    request.setValue("application/json", forHTTPHeaderField: "Content-Type")
                    var body: [String: Any] = ["query": query, "stream": true]
                    if let chatSessionID {
                        body["chatSessionId"] = chatSessionID
                    }
                    if let projectIDs, !projectIDs.isEmpty {
                        body["projectIds"] = projectIDs
                    }
                    if let timeFilter {
                        body["timeFilter"] = timeFilter
                    }
                    if let agentNames, !agentNames.isEmpty {
                        body["agentNames"] = agentNames
                    }
                    request.httpBody = try JSONSerialization.data(withJSONObject: body)

                    let (bytes, response) = try await self.session.bytes(for: request)
                    guard let http = response as? HTTPURLResponse else {
                        throw ChatStreamError.invalidResponse
                    }
                    guard (200..<300).contains(http.statusCode) else {
                        var errorBody = Data()
                        for try await byte in bytes {
                            errorBody.append(byte)
                            if errorBody.count >= 4096 { break }
                        }
                        throw ChatStreamError.http(
                            status: http.statusCode,
                            body: String(data: errorBody, encoding: .utf8) ?? ""
                        )
                    }

                    var buffer = NDJSONLineBuffer()
                    // AsyncBytes yields single bytes; batch up to each newline
                    // before feeding the line buffer.
                    var batch = Data()
                    for try await byte in bytes {
                        batch.append(byte)
                        if byte == 0x0A {
                            for line in buffer.append(batch) {
                                Self.emit(line: line, to: continuation)
                            }
                            batch.removeAll(keepingCapacity: true)
                        }
                    }
                    if !batch.isEmpty {
                        for line in buffer.append(batch) {
                            Self.emit(line: line, to: continuation)
                        }
                    }
                    // Known VSIX bug: the final line often arrives without a
                    // trailing newline and was dropped. Always flush.
                    if let tail = buffer.flush() {
                        Self.emit(line: tail, to: continuation)
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in
                task.cancel()
            }
        }
    }

    private static func emit(
        line: String,
        to continuation: AsyncThrowingStream<ChatStreamEvent, Error>.Continuation
    ) {
        if let event = ChatStreamEvent.parse(line: line) {
            continuation.yield(event)
        }
    }

    // MARK: - Chat threads

    public func chats(limit: Int) async throws -> [ChatThreadSummary] {
        struct Payload: Decodable {
            let chatSessions: [ChatThreadSummary]
        }
        let data = try await getWithRetry(
            path: "chats",
            queryItems: [URLQueryItem(name: "limit", value: String(limit))]
        )
        return try Self.unwrap(Payload.self, from: data).chatSessions
    }

    public func chat(id: String) async throws -> ChatThread {
        struct Payload: Decodable {
            let chatSession: ChatThread
        }
        let data = try await getWithRetry(path: "chat/\(id)")
        return try Self.unwrap(Payload.self, from: data).chatSession
    }

    public func deleteChat(id: String) async throws {
        var request = try await authorizedRequest(path: "chat/\(id)")
        request.httpMethod = "DELETE"
        let (data, http) = try await perform(request)
        try Self.checkStatus(http, data: data)
    }

    /// Resolves an inline [chunk:ID] citation to its content and origin.
    public func chunk(id: String) async throws -> ChunkDetail {
        let data = try await getWithRetry(path: "chunks/\(id)")
        return try Self.unwrap(ChunkDetail.self, from: data)
    }

    // MARK: - Plumbing

    private func authorizedRequest(path: String, queryItems: [URLQueryItem] = []) async throws -> URLRequest {
        guard let token = try await accessTokenProvider() else {
            throw ChatStreamError.signedOut
        }
        var url = baseURL.appendingPathComponent(path)
        if !queryItems.isEmpty,
           var components = URLComponents(url: url, resolvingAgainstBaseURL: false) {
            components.queryItems = queryItems
            url = components.url ?? url
        }
        var request = URLRequest(url: url)
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        return request
    }

    /// Non-streaming reads retry twice on 5xx/429 with 300/900 ms backoff.
    private func getWithRetry(path: String, queryItems: [URLQueryItem] = []) async throws -> Data {
        let backoffs: [UInt64] = [300_000_000, 900_000_000]
        var attempt = 0
        while true {
            let request = try await authorizedRequest(path: path, queryItems: queryItems)
            let (data, http) = try await perform(request)
            if http.statusCode >= 500 || http.statusCode == 429, attempt < backoffs.count {
                try await Task.sleep(nanoseconds: backoffs[attempt])
                attempt += 1
                continue
            }
            try Self.checkStatus(http, data: data)
            return data
        }
    }

    private func perform(_ request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else {
            throw ChatStreamError.invalidResponse
        }
        return (data, http)
    }

    private static func checkStatus(_ http: HTTPURLResponse, data: Data) throws {
        guard (200..<300).contains(http.statusCode) else {
            throw ChatStreamError.http(
                status: http.statusCode,
                body: String(data: data, encoding: .utf8) ?? ""
            )
        }
    }

    /// All non-streaming endpoints wrap payloads in {"success":true,"data":...}.
    private struct Envelope<T: Decodable>: Decodable {
        let data: T
    }

    private static func unwrap<T: Decodable>(_ type: T.Type, from data: Data) throws -> T {
        do {
            return try JSONDecoder().decode(Envelope<T>.self, from: data).data
        } catch {
            throw ChatStreamError.decoding(String(describing: error))
        }
    }
}
