import Foundation

/// Errors for the Pro-gated analytics endpoints. The worker stacks two gates
/// on every route: the environment flag answers 404 (the feature "does not
/// exist" -> .featureDark) above the plan entitlement's 403 upgrade_required
/// (-> .upgradeRequired), so callers can route both to gate UI instead of
/// error banners.
public enum CloudAnalyticsError: Error {
    case unauthorized                       // 401 after retries
    case upgradeRequired(feature: String?)  // 403 {"error":"upgrade_required","data":{"feature"}}
    case featureDark                        // 404: the environment flag is off
    case http(status: Int, body: String)
    case network(Error)
    case decoding(Error)
}

extension CloudAnalyticsError: LocalizedError {
    public var errorDescription: String? {
        switch self {
        case .unauthorized:
            return "Your SpecStory Cloud session has expired. Please sign in again."
        case .upgradeRequired:
            return "Analytics requires a SpecStory Pro plan."
        case .featureDark:
            return "Analytics is not available yet."
        case .http(let status, _):
            return "SpecStory Cloud request failed (HTTP \(status))."
        case .network:
            return "Could not reach SpecStory Cloud. Check your connection and try again."
        case .decoding:
            return "Received an unexpected response from SpecStory Cloud."
        }
    }
}

/// Client for `GET /api/v1/analytics/*`. Same wire conventions as CloudAPI:
/// Bearer access token, {"success":true,"data":...} envelope, reads retry
/// twice on 5xx/429 with 300/900 ms backoff. All endpoints accept the
/// cross-tab `agent`/`project` filters.
public actor CloudAnalyticsAPI {
    private let apiRoot: URL
    private let session: URLSession
    private let accessTokenProvider: () async throws -> String?
    private let decoder = JSONDecoder()

    private static let retryDelays: [TimeInterval] = [0.3, 0.9]

    public init(baseURL: URL, accessTokenProvider: @escaping () async throws -> String?) {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
        configuration.urlCache = nil
        self.init(baseURL: baseURL, configuration: configuration, accessTokenProvider: accessTokenProvider)
    }

    /// Test hook: inject a URLSessionConfiguration carrying a stub URLProtocol.
    init(baseURL: URL, configuration: URLSessionConfiguration, accessTokenProvider: @escaping () async throws -> String?) {
        if baseURL.path.hasSuffix("/api/v1") {
            self.apiRoot = baseURL
        } else {
            self.apiRoot = baseURL.appendingPathComponent("api/v1")
        }
        self.session = URLSession(configuration: configuration)
        self.accessTokenProvider = accessTokenProvider
    }

    // MARK: - Endpoints

    /// `GET /analytics/overview?range=&tz=`. Send the caller's IANA zone so the
    /// punchcard buckets locally; everything else is UTC regardless.
    public func overview(range: AnalyticsRange, timezone: String? = nil,
                         agent: String? = nil, project: String? = nil) async throws -> AnalyticsOverview {
        var query = [URLQueryItem(name: "range", value: range.rawValue)]
        if let timezone, !timezone.isEmpty {
            query.append(URLQueryItem(name: "tz", value: timezone))
        }
        query.append(contentsOf: filterItems(agent: agent, project: project))
        let data = try await read("analytics/overview", query: query)
        struct Payload: Decodable { let overview: AnalyticsOverview? }
        if let payload = try? decoder.decode(APIEnvelope<Payload>.self, from: data), let overview = payload.data?.overview {
            return overview
        }
        return try unwrapEnvelope(AnalyticsOverview.self, from: data)
    }

    /// `GET /analytics/sessions?from=&to=`. Drill-down rows, cap 500 newest
    /// first; `from`/`to` accept epoch-ms or ISO, `to` exclusive, defaulting
    /// to the trailing 30 days.
    public func sessions(fromIso: String? = nil, toIso: String? = nil,
                         agent: String? = nil, project: String? = nil) async throws -> [AnalyticsSessionRow] {
        var query: [URLQueryItem] = []
        if let fromIso, !fromIso.isEmpty { query.append(URLQueryItem(name: "from", value: fromIso)) }
        if let toIso, !toIso.isEmpty { query.append(URLQueryItem(name: "to", value: toIso)) }
        query.append(contentsOf: filterItems(agent: agent, project: project))
        let data = try await read("analytics/sessions", query: query)
        struct Payload: Decodable { let sessions: [AnalyticsSessionRow]? }
        if let payload = try? decoder.decode(APIEnvelope<Payload>.self, from: data), let sessions = payload.data?.sessions {
            return sessions
        }
        return try unwrapEnvelope([AnalyticsSessionRow].self, from: data)
    }

    /// `GET /analytics/ledger?range=`. Estimated public-rate spend.
    public func ledger(range: AnalyticsRange, agent: String? = nil, project: String? = nil) async throws -> AnalyticsLedger {
        var query = [URLQueryItem(name: "range", value: range.rawValue)]
        query.append(contentsOf: filterItems(agent: agent, project: project))
        let data = try await read("analytics/ledger", query: query)
        struct Payload: Decodable { let ledger: AnalyticsLedger? }
        if let payload = try? decoder.decode(APIEnvelope<Payload>.self, from: data), let ledger = payload.data?.ledger {
            return ledger
        }
        return try unwrapEnvelope(AnalyticsLedger.self, from: data)
    }

    /// `GET /analytics/spec-score?range=`. Opener grades from the effective
    /// verdict (LLM judge with deterministic fallback).
    public func specScore(range: AnalyticsRange, agent: String? = nil, project: String? = nil) async throws -> AnalyticsSpecScore {
        var query = [URLQueryItem(name: "range", value: range.rawValue)]
        query.append(contentsOf: filterItems(agent: agent, project: project))
        let data = try await read("analytics/spec-score", query: query)
        struct Payload: Decodable { let specScore: AnalyticsSpecScore? }
        if let payload = try? decoder.decode(APIEnvelope<Payload>.self, from: data), let score = payload.data?.specScore {
            return score
        }
        return try unwrapEnvelope(AnalyticsSpecScore.self, from: data)
    }

    /// `GET /analytics/spec-score/sessions?range=&grade=&limit=&offset=`.
    /// Grade-filtered browser behind the distribution bars; newest first, not
    /// deduped; server default limit 25, max 100.
    public func specScoreSessions(range: AnalyticsRange, grade: String,
                                  limit: Int = 25, offset: Int = 0) async throws -> SpecScoreSessionsPage {
        let query = [
            URLQueryItem(name: "range", value: range.rawValue),
            URLQueryItem(name: "grade", value: grade),
            URLQueryItem(name: "limit", value: String(limit)),
            URLQueryItem(name: "offset", value: String(offset)),
        ]
        let data = try await read("analytics/spec-score/sessions", query: query)
        struct Payload: Decodable { let specScoreSessions: SpecScoreSessionsPage? }
        if let payload = try? decoder.decode(APIEnvelope<Payload>.self, from: data), let page = payload.data?.specScoreSessions {
            return page
        }
        return try unwrapEnvelope(SpecScoreSessionsPage.self, from: data)
    }

    /// `GET /analytics/processing`. Cheap derive-pipeline poll.
    public func processing() async throws -> AnalyticsProcessing {
        let data = try await read("analytics/processing")
        struct Payload: Decodable { let processing: AnalyticsProcessing? }
        if let payload = try? decoder.decode(APIEnvelope<Payload>.self, from: data), let processing = payload.data?.processing {
            return processing
        }
        return try unwrapEnvelope(AnalyticsProcessing.self, from: data)
    }

    // MARK: - Request plumbing

    private func filterItems(agent: String?, project: String?) -> [URLQueryItem] {
        var items: [URLQueryItem] = []
        if let agent, !agent.isEmpty { items.append(URLQueryItem(name: "agent", value: agent)) }
        if let project, !project.isEmpty { items.append(URLQueryItem(name: "project", value: project)) }
        return items
    }

    private func url(_ path: String, query: [URLQueryItem]) -> URL {
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

    private func authorizedRequest(_ path: String, query: [URLQueryItem]) async throws -> URLRequest {
        guard let token = try await accessTokenProvider() else {
            throw CloudAnalyticsError.unauthorized
        }
        var request = URLRequest(url: url(path, query: query))
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        return request
    }

    private func perform(_ request: URLRequest, retries: Int) async throws -> (Data, HTTPURLResponse) {
        var attempt = 0
        while true {
            do {
                let (data, response) = try await session.data(for: request)
                guard let http = response as? HTTPURLResponse else {
                    throw CloudAnalyticsError.network(URLError(.badServerResponse))
                }
                let retryable = http.statusCode >= 500 || http.statusCode == 429
                if retryable && attempt < retries {
                    try? await Task.sleep(nanoseconds: UInt64(Self.retryDelays[min(attempt, Self.retryDelays.count - 1)] * 1_000_000_000))
                    attempt += 1
                    continue
                }
                return (data, http)
            } catch let error as CloudAnalyticsError {
                throw error
            } catch {
                throw CloudAnalyticsError.network(error)
            }
        }
    }

    /// GET with read retries; returns the raw body after mapping status errors.
    private func read(_ path: String, query: [URLQueryItem] = []) async throws -> Data {
        let request = try await authorizedRequest(path, query: query)
        let (data, response) = try await perform(request, retries: Self.retryDelays.count)
        guard (200...299).contains(response.statusCode) else {
            throw statusError(response.statusCode, data: data)
        }
        return data
    }

    private func unwrapEnvelope<T: Decodable>(_ type: T.Type, from data: Data) throws -> T {
        let envelope: APIEnvelope<T>
        do {
            envelope = try decoder.decode(APIEnvelope<T>.self, from: data)
        } catch {
            throw CloudAnalyticsError.decoding(error)
        }
        if let payload = envelope.data, envelope.success {
            return payload
        }
        throw CloudAnalyticsError.http(status: 200, body: envelope.error ?? "missing data in response envelope")
    }

    /// Gate layering per the worker: the environment flag (404) sits above the
    /// entitlement (403 upgrade_required).
    private func statusError(_ status: Int, data: Data) -> CloudAnalyticsError {
        switch status {
        case 401:
            return .unauthorized
        case 403:
            return .upgradeRequired(feature: upgradeFeature(in: data))
        case 404:
            return .featureDark
        default:
            return .http(status: status, body: String(data: data, encoding: .utf8) ?? "")
        }
    }

    private func upgradeFeature(in data: Data) -> String? {
        struct FeatureData: Decodable { let feature: String? }
        struct Body: Decodable { let error: String?; let data: FeatureData? }
        return (try? decoder.decode(Body.self, from: data))?.data?.feature
    }
}
