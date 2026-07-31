import Foundation

public enum CloudAPIError: Error {
    case unauthorized            // 401 after retries
    case forbidden               // 403 (upgrade-required states)
    case notFound                // 404
    case http(status: Int, body: String)
    case network(Error)
    case decoding(Error)
    /// Device login failures carry user-visible copy distinguishing
    /// invalid, already used, expired, and malformed codes.
    case deviceCodeRejected(message: String)
}

extension CloudAPIError: LocalizedError {
    public var errorDescription: String? {
        switch self {
        case .unauthorized:
            return "Your SpecStory Cloud session has expired. Please sign in again."
        case .forbidden:
            return "Your plan does not include this feature."
        case .notFound:
            return "That item was not found in SpecStory Cloud."
        case .http(let status, _):
            return "SpecStory Cloud request failed (HTTP \(status))."
        case .network:
            return "Could not reach SpecStory Cloud. Check your connection and try again."
        case .decoding:
            return "Received an unexpected response from SpecStory Cloud."
        case .deviceCodeRejected(let message):
            return message
        }
    }
}

/// SpecStory Cloud REST + GraphQL client. Base https://cloud.specstory.com,
/// all endpoints under /api/v1, responses wrapped in {"success":true,"data":...}.
public actor CloudAPI {
    private let apiRoot: URL
    private let session: URLSession
    private let accessTokenProvider: () async throws -> String?
    private let decoder = JSONDecoder()

    /// Retry backoff for reads on 5xx/429: 300 ms then 900 ms.
    private static let retryDelays: [TimeInterval] = [0.3, 0.9]

    public init(baseURL: URL, accessTokenProvider: @escaping () async throws -> String?) {
        // Caching is disabled so 304 handling stays explicit via If-None-Match.
        let configuration = URLSessionConfiguration.ephemeral
        configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
        configuration.urlCache = nil
        self.init(baseURL: baseURL, configuration: configuration, accessTokenProvider: accessTokenProvider)
    }

    /// Test hook: inject a URLSessionConfiguration carrying a stub URLProtocol.
    init(baseURL: URL, configuration: URLSessionConfiguration, accessTokenProvider: @escaping () async throws -> String?) {
        // Accept either a host root or a URL already ending in /api/v1.
        if baseURL.path.hasSuffix("/api/v1") {
            self.apiRoot = baseURL
        } else {
            self.apiRoot = baseURL.appendingPathComponent("api/v1")
        }
        self.session = URLSession(configuration: configuration)
        self.accessTokenProvider = accessTokenProvider
    }

    // MARK: - Device auth

    public func deviceLogin(code: String, metadata: DeviceMetadata) async throws -> DeviceLoginResult {
        // Codes display as "ABC-123"; the dash is cosmetic. Case matters.
        let normalized = code
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .replacingOccurrences(of: "-", with: "")
        guard normalized.count == 6, normalized.allSatisfy({ $0.isASCII && ($0.isLetter || $0.isNumber) }) else {
            throw CloudAPIError.deviceCodeRejected(message: Self.malformedCodeCopy)
        }

        var request = URLRequest(url: url("device-login"))
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try encodeJSON(DeviceLoginRequest(deviceCode: normalized, metadata: metadata))

        let (data, response) = try await perform(request, retries: 0)
        switch response.statusCode {
        case 200...299:
            return try unwrapEnvelope(DeviceLoginResult.self, from: data, status: response.statusCode)
        case 400:
            throw CloudAPIError.deviceCodeRejected(message: Self.malformedCodeCopy)
        case 401:
            throw CloudAPIError.deviceCodeRejected(message: Self.loginCopy(forServerError: serverError(in: data)))
        default:
            throw statusError(response.statusCode, data: data)
        }
    }

    public func deviceRefresh(refreshToken: String) async throws -> AccessTokenResult {
        var request = URLRequest(url: url("device-refresh"))
        request.httpMethod = "POST"
        request.setValue("Bearer \(refreshToken)", forHTTPHeaderField: "Authorization")
        let (data, response) = try await perform(request, retries: 0)
        guard (200...299).contains(response.statusCode) else {
            throw statusError(response.statusCode, data: data)
        }
        return try unwrapEnvelope(AccessTokenResult.self, from: data, status: response.statusCode)
    }

    public func deviceLogout(refreshToken: String) async {
        var request = URLRequest(url: url("device-logout"))
        request.httpMethod = "POST"
        request.setValue("Bearer \(refreshToken)", forHTTPHeaderField: "Authorization")
        // Best effort by contract: the server answers {"success":true} regardless,
        // and local sign-out proceeds even if this never lands.
        _ = try? await perform(request, retries: 0)
    }

    // MARK: - Reads

    public func recentSessions(limit: Int) async throws -> [CloudSession] {
        struct Payload: Decodable { let sessions: [CloudSession] }
        let request = try await authorizedRequest("sessions/recent", query: [URLQueryItem(name: "limit", value: String(limit))])
        let data = try await readEnvelopeData(request)
        if let payload = try? decoder.decode(APIEnvelope<Payload>.self, from: data), let inner = payload.data {
            return inner.sessions
        }
        return try unwrapEnvelope([CloudSession].self, from: data, status: 200)
    }

    public func projects() async throws -> [CloudProject] {
        struct Payload: Decodable { let projects: [CloudProject] }
        let request = try await authorizedRequest("projects")
        let data = try await readEnvelopeData(request)
        if let payload = try? decoder.decode(APIEnvelope<Payload>.self, from: data), let inner = payload.data {
            return inner.projects
        }
        return try unwrapEnvelope([CloudProject].self, from: data, status: 200)
    }

    public func sessions(projectID: String, limit: Int) async throws -> [CloudSession] {
        struct Payload: Decodable { let sessions: [CloudSession] }
        let request = try await authorizedRequest(
            "projects/\(escape(projectID))/sessions",
            query: [URLQueryItem(name: "limit", value: String(limit))]
        )
        let data = try await readEnvelopeData(request)
        if let payload = try? decoder.decode(APIEnvelope<Payload>.self, from: data), let inner = payload.data {
            return inner.sessions
        }
        return try unwrapEnvelope([CloudSession].self, from: data, status: 200)
    }

    public func sessionMarkdown(projectID: String, sessionID: String, etag: String?) async throws -> SessionMarkdown {
        var request = try await authorizedRequest("projects/\(escape(projectID))/sessions/\(escape(sessionID))")
        request.setValue("text/markdown", forHTTPHeaderField: "Accept")
        if let etag, !etag.isEmpty {
            request.setValue(etag, forHTTPHeaderField: "If-None-Match")
        }
        let (data, response) = try await perform(request, retries: Self.retryDelays.count)
        if response.statusCode == 304 {
            return .notModified
        }
        guard (200...299).contains(response.statusCode) else {
            throw statusError(response.statusCode, data: data)
        }
        let markdown = String(data: data, encoding: .utf8) ?? ""
        return .content(markdown: markdown, etag: response.value(forHTTPHeaderField: "ETag"))
    }

    public func sessionHead(projectID: String, sessionID: String) async throws -> SessionHead {
        var request = try await authorizedRequest("projects/\(escape(projectID))/sessions/\(escape(sessionID))")
        request.httpMethod = "HEAD"
        let (data, response) = try await perform(request, retries: Self.retryDelays.count)
        guard (200...299).contains(response.statusCode) else {
            throw statusError(response.statusCode, data: data)
        }
        func intHeader(_ name: String) -> Int {
            Int(response.value(forHTTPHeaderField: name) ?? "") ?? 0
        }
        return SessionHead(
            etag: response.value(forHTTPHeaderField: "ETag"),
            markdownSize: intHeader("X-Markdown-Size"),
            rawDataSize: intHeader("X-Raw-Data-Size"),
            sessionDataSize: intHeader("X-Session-Data-Size"),
            lastModified: response.value(forHTTPHeaderField: "Last-Modified")
        )
    }

    public func entitlement() async throws -> Entitlement {
        let request = try await authorizedRequest("billing/entitlement")
        let data = try await readEnvelopeData(request)
        return try unwrapEnvelope(Entitlement.self, from: data, status: 200)
    }

    /// Feature flags fail open: any error at all yields an empty dictionary
    /// so flag-gated UI falls back to its defaults instead of breaking.
    public func flags() async throws -> [String: Bool] {
        do {
            let request = try await authorizedRequest("flags")
            let data = try await readEnvelopeData(request)
            if let direct = try? decoder.decode(APIEnvelope<[String: Bool]>.self, from: data), let flags = direct.data {
                return flags
            }
            struct Payload: Decodable { let flags: [String: Bool] }
            if let nested = try? decoder.decode(APIEnvelope<Payload>.self, from: data), let inner = nested.data {
                return inner.flags
            }
            return [:]
        } catch {
            return [:]
        }
    }

    public func userTools() async throws -> [UserTool] {
        struct Payload: Decodable { let tools: [UserTool] }
        let request = try await authorizedRequest("user/tools")
        let data = try await readEnvelopeData(request)
        if let payload = try? decoder.decode(APIEnvelope<Payload>.self, from: data), let inner = payload.data {
            return inner.tools
        }
        return try unwrapEnvelope([UserTool].self, from: data, status: 200)
    }

    public func searchSessions(query: String, projectIDs: [String]?, timeFilter: String?, agentNames: [String]?, limit: Int) async throws -> [SearchHit] {
        // Only projectIds, timeFilter, and agentNames filter server-side.
        var filters: [String: Any] = [:]
        if let projectIDs, !projectIDs.isEmpty { filters["projectIds"] = projectIDs }
        if let timeFilter, !timeFilter.isEmpty { filters["timeFilter"] = timeFilter }
        if let agentNames, !agentNames.isEmpty { filters["agentNames"] = agentNames }

        let graphQuery = """
        query SearchSessions($query: String!, $filters: SessionFilters, $limit: Int) {
          searchSessions(query: $query, filters: $filters, limit: $limit) {
            total
            results {
              id
              clientId
              projectId
              name
              userTitle
              rank
              matchingExchanges { id content orderNumber }
              project { id name icon color }
            }
          }
        }
        """
        var variables: [String: Any] = ["query": query, "limit": limit]
        if !filters.isEmpty { variables["filters"] = filters }
        let body = try JSONSerialization.data(withJSONObject: ["query": graphQuery, "variables": variables])

        var request = try await authorizedRequest("graphql")
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = body

        let (data, response) = try await perform(request, retries: Self.retryDelays.count)
        guard (200...299).contains(response.statusCode) else {
            throw statusError(response.statusCode, data: data)
        }

        struct SearchPayload: Decodable { let total: Int?; let results: [SearchHit]? }
        struct GraphData: Decodable { let searchSessions: SearchPayload? }
        struct GraphError: Decodable { let message: String? }
        struct GraphResponse: Decodable { let data: GraphData?; let errors: [GraphError]? }

        let decoded: GraphResponse
        do {
            decoded = try decoder.decode(GraphResponse.self, from: data)
        } catch {
            throw CloudAPIError.decoding(error)
        }
        if let hits = decoded.data?.searchSessions?.results {
            return hits
        }
        let message = decoded.errors?.compactMap(\.message).joined(separator: "; ") ?? "empty GraphQL response"
        if message.localizedCaseInsensitiveContains("unauthorized") {
            throw CloudAPIError.unauthorized
        }
        throw CloudAPIError.http(status: response.statusCode, body: message)
    }

    public func updateSession(projectID: String, sessionID: String, userTitle: String?, shareStatus: String?) async throws {
        var patch: [String: String] = [:]
        if let userTitle { patch["userTitle"] = userTitle }
        if let shareStatus { patch["shareStatus"] = shareStatus }
        // The server requires at least one field; nothing to send is a no-op.
        guard !patch.isEmpty else { return }

        var request = try await authorizedRequest("projects/\(escape(projectID))/sessions/\(escape(sessionID))")
        request.httpMethod = "PATCH"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONSerialization.data(withJSONObject: patch)

        let (data, response) = try await perform(request, retries: 0)
        guard (200...299).contains(response.statusCode) else {
            throw statusError(response.statusCode, data: data)
        }
    }

    // MARK: - Request plumbing

    private func url(_ path: String, query: [URLQueryItem] = []) -> URL {
        var target = apiRoot
        for component in path.split(separator: "/") {
            target.appendPathComponent(String(component))
        }
        if !query.isEmpty, var components = URLComponents(url: target, resolvingAgainstBaseURL: false) {
            components.queryItems = query
            if let withQuery = components.url {
                target = withQuery
            }
        }
        return target
    }

    private func escape(_ pathComponent: String) -> String {
        // appendPathComponent would double-escape; strip separators instead so
        // client identifiers cannot break out of their path slot.
        pathComponent.replacingOccurrences(of: "/", with: "-")
    }

    private func authorizedRequest(_ path: String, query: [URLQueryItem] = []) async throws -> URLRequest {
        guard let token = try await accessTokenProvider() else {
            throw CloudAPIError.unauthorized
        }
        var request = URLRequest(url: url(path, query: query))
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        return request
    }

    /// Executes a request, retrying on 5xx/429 up to `retries` extra attempts.
    private func perform(_ request: URLRequest, retries: Int) async throws -> (Data, HTTPURLResponse) {
        var attempt = 0
        while true {
            do {
                let (data, response) = try await session.data(for: request)
                guard let http = response as? HTTPURLResponse else {
                    throw CloudAPIError.network(URLError(.badServerResponse))
                }
                let retryable = http.statusCode >= 500 || http.statusCode == 429
                if retryable && attempt < retries {
                    try? await Task.sleep(nanoseconds: UInt64(Self.retryDelays[min(attempt, Self.retryDelays.count - 1)] * 1_000_000_000))
                    attempt += 1
                    continue
                }
                return (data, http)
            } catch let error as CloudAPIError {
                throw error
            } catch {
                throw CloudAPIError.network(error)
            }
        }
    }

    /// GET with read retries; returns raw body after mapping status errors.
    private func readEnvelopeData(_ request: URLRequest) async throws -> Data {
        let (data, response) = try await perform(request, retries: Self.retryDelays.count)
        guard (200...299).contains(response.statusCode) else {
            throw statusError(response.statusCode, data: data)
        }
        return data
    }

    private func unwrapEnvelope<T: Decodable>(_ type: T.Type, from data: Data, status: Int) throws -> T {
        let envelope: APIEnvelope<T>
        do {
            envelope = try decoder.decode(APIEnvelope<T>.self, from: data)
        } catch {
            throw CloudAPIError.decoding(error)
        }
        if let payload = envelope.data, envelope.success {
            return payload
        }
        throw CloudAPIError.http(status: status, body: envelope.error ?? "missing data in response envelope")
    }

    private func statusError(_ status: Int, data: Data) -> CloudAPIError {
        switch status {
        case 401: return .unauthorized
        case 403: return .forbidden
        case 404: return .notFound
        default: return .http(status: status, body: String(data: data, encoding: .utf8) ?? "")
        }
    }

    private func serverError(in data: Data) -> String {
        struct ErrorOnly: Decodable { let error: String? }
        return (try? decoder.decode(ErrorOnly.self, from: data))?.error ?? ""
    }

    private func encodeJSON<T: Encodable>(_ value: T) throws -> Data {
        do {
            return try JSONEncoder().encode(value)
        } catch {
            throw CloudAPIError.decoding(error)
        }
    }

    // MARK: - Device login copy

    static let malformedCodeCopy = "Enter the 6 character code shown in your browser, like ABC-123."

    static func loginCopy(forServerError serverError: String) -> String {
        let lowered = serverError.lowercased()
        if lowered.contains("used") {
            return "That code has already been used. Generate a new code in your browser and try again."
        }
        // The server's catch-all is "Invalid or expired device code"; only a
        // message that names expiry alone is a definite expiry.
        if lowered.contains("expired") && !lowered.contains("invalid") {
            return "That code has expired. Generate a new code in your browser and try again."
        }
        return "That code was not recognized. Check it and try again, or generate a new code in your browser."
    }
}

/// device-login body: device_code plus flattened device metadata.
private struct DeviceLoginRequest: Encodable {
    let deviceCode: String
    let metadata: DeviceMetadata

    private enum CodingKeys: String, CodingKey {
        case deviceCode = "device_code"
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(deviceCode, forKey: .deviceCode)
        try metadata.encode(to: encoder)
    }
}
