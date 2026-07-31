import Foundation

/// Errors for Pro-gated endpoints. The two expected degrade states get their
/// own cases so callers can route them to upsell or hidden UI instead of
/// treating them as failures.
public enum CloudProError: Error {
    case unauthorized                       // 401 after retries
    case upgradeRequired(feature: String?)  // 403 {"error":"upgrade_required","data":{"feature"}}
    case featureDark                        // 404: the environment flag is off, the feature "does not exist"
    case http(status: Int, body: String)
    case network(Error)
    case decoding(Error)
}

extension CloudProError: LocalizedError {
    public var errorDescription: String? {
        switch self {
        case .unauthorized:
            return "Your SpecStory Cloud session has expired. Please sign in again."
        case .upgradeRequired:
            return "This feature requires a SpecStory Pro plan."
        case .featureDark:
            return "This feature is not available yet."
        case .http(let status, _):
            return "SpecStory Cloud request failed (HTTP \(status))."
        case .network:
            return "Could not reach SpecStory Cloud. Check your connection and try again."
        case .decoding:
            return "Received an unexpected response from SpecStory Cloud."
        }
    }
}

/// Client for the Pro feature surface: entitlement gates, the skills library,
/// cloud resume discovery, and billing URLs. Same wire conventions as
/// CloudAPI: base /api/v1, Bearer access token, {"success":true,"data":...}
/// envelope (skills endpoints answer top-level {success, skills}), reads retry
/// twice on 5xx/429 with 300/900 ms backoff.
public actor CloudProAPI {
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

    // MARK: - Gate inputs

    /// Never flag-gated; entitlement fails closed at the call site (treat any
    /// error as no entitlement).
    public func entitlement() async throws -> Entitlement {
        let request = try await authorizedRequest("billing/entitlement")
        let data = try await readData(request)
        return try unwrapEnvelope(Entitlement.self, from: data)
    }

    /// Feature flags fail open: any error yields an empty dictionary so a
    /// missing flag counts as on.
    public func flags() async -> [String: Bool] {
        do {
            let request = try await authorizedRequest("flags")
            let data = try await readData(request)
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

    // MARK: - Skills library

    /// GET /api/v1/lore/skills/library: the unified library (forged skills
    /// merged with pending review dossiers). Answers top-level
    /// {success, skills:[...]}, not the data envelope.
    public func skillsLibrary() async throws -> [SkillRow] {
        let request = try await authorizedRequest("lore/skills/library")
        let data = try await readData(request)
        struct Library: Decodable { let success: Bool?; let skills: [SkillRow]? }
        if let library = try? decoder.decode(Library.self, from: data), let skills = library.skills {
            return skills
        }
        // Tolerate an enveloped variant should the worker ever normalize it.
        struct Payload: Decodable { let skills: [SkillRow]? }
        if let nested = try? decoder.decode(APIEnvelope<Payload>.self, from: data), let skills = nested.data?.skills {
            return skills
        }
        return try unwrapEnvelope([SkillRow].self, from: data)
    }

    /// PATCH /api/v1/lore/skills/:name {install_state}: the client flips this
    /// after a local install or uninstall.
    public func setSkillInstallState(name: String, installed: Bool) async throws {
        var request = try await authorizedRequest("lore/skills/\(escape(name))")
        request.httpMethod = "PATCH"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONSerialization.data(withJSONObject: [
            "install_state": installed ? "installed" : "available"
        ])
        let (data, response) = try await perform(request, retries: 0)
        guard (200...299).contains(response.statusCode) else {
            throw statusError(response.statusCode, data: data)
        }
    }

    // MARK: - Cloud resume discovery

    /// GET /api/v1/projects?resumable=true: projects with their resumable
    /// session summaries inlined; projects without resumable sessions are
    /// dropped server-side. Pro-gated only because of the query parameter.
    public func resumableProjects() async throws -> [ResumableProject] {
        struct Payload: Decodable { let projects: [ResumableProject]?; let total: Int? }
        let request = try await authorizedRequest("projects", query: [URLQueryItem(name: "resumable", value: "true")])
        let data = try await readData(request)
        if let payload = try? decoder.decode(APIEnvelope<Payload>.self, from: data), let projects = payload.data?.projects {
            return projects
        }
        if let flat = try? decoder.decode(Payload.self, from: data), let projects = flat.projects {
            return projects
        }
        return try unwrapEnvelope([ResumableProject].self, from: data)
    }

    /// GET /api/v1/projects/:projectId/sessions?resumable=true. The resumable
    /// listing is uncapped server-side.
    public func resumableSessions(projectID: String) async throws -> [ResumableSessionSummary] {
        struct Payload: Decodable { let sessions: [ResumableSessionSummary]?; let total: Int? }
        let request = try await authorizedRequest(
            "projects/\(escape(projectID))/sessions",
            query: [URLQueryItem(name: "resumable", value: "true")]
        )
        let data = try await readData(request)
        if let payload = try? decoder.decode(APIEnvelope<Payload>.self, from: data), let sessions = payload.data?.sessions {
            return sessions
        }
        if let flat = try? decoder.decode(Payload.self, from: data), let sessions = flat.sessions {
            return sessions
        }
        return try unwrapEnvelope([ResumableSessionSummary].self, from: data)
    }

    /// POST /api/v1/search/resume {query, projectId?}. Snippet highlight
    /// markers (STX/ETX) are stripped into HighlightedSnippet spans during
    /// decode. Server-side search failures degrade to empty results.
    public func searchResume(query: String, projectID: String? = nil) async throws -> [ResumeSearchHit] {
        var body: [String: Any] = ["query": query]
        if let projectID, !projectID.isEmpty { body["projectId"] = projectID }

        var request = try await authorizedRequest("search/resume")
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONSerialization.data(withJSONObject: body)

        let (data, response) = try await perform(request, retries: Self.retryDelays.count)
        guard (200...299).contains(response.statusCode) else {
            throw statusError(response.statusCode, data: data)
        }
        struct Payload: Decodable { let results: [ResumeSearchHit]? }
        if let payload = try? decoder.decode(APIEnvelope<Payload>.self, from: data), let results = payload.data?.results {
            return results
        }
        return try unwrapEnvelope([ResumeSearchHit].self, from: data)
    }

    // MARK: - Billing

    /// POST /api/v1/billing/checkout {plan}: a Stripe Checkout URL, or
    /// alreadySubscribed when there is a live subscription.
    public func checkout(plan: Plan) async throws -> CheckoutResult {
        var request = try await authorizedRequest("billing/checkout")
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONSerialization.data(withJSONObject: ["plan": plan.rawValue])

        let (data, response) = try await perform(request, retries: 0)
        guard (200...299).contains(response.statusCode) else {
            throw statusError(response.statusCode, data: data)
        }
        return try unwrapEnvelope(CheckoutResult.self, from: data)
    }

    /// POST /api/v1/billing/portal: the Stripe Customer Portal URL. 404 when
    /// the user has no billing account (surfaces as .featureDark).
    public func billingPortalURL() async throws -> URL {
        var request = try await authorizedRequest("billing/portal")
        request.httpMethod = "POST"

        let (data, response) = try await perform(request, retries: 0)
        guard (200...299).contains(response.statusCode) else {
            throw statusError(response.statusCode, data: data)
        }
        struct Payload: Decodable { let url: String? }
        let payload = try unwrapEnvelope(Payload.self, from: data)
        guard let raw = payload.url, let url = URL(string: raw) else {
            throw CloudProError.decoding(URLError(.badURL))
        }
        return url
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
        // identifiers cannot break out of their path slot.
        pathComponent.replacingOccurrences(of: "/", with: "-")
    }

    private func authorizedRequest(_ path: String, query: [URLQueryItem] = []) async throws -> URLRequest {
        guard let token = try await accessTokenProvider() else {
            throw CloudProError.unauthorized
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
                    throw CloudProError.network(URLError(.badServerResponse))
                }
                let retryable = http.statusCode >= 500 || http.statusCode == 429
                if retryable && attempt < retries {
                    try? await Task.sleep(nanoseconds: UInt64(Self.retryDelays[min(attempt, Self.retryDelays.count - 1)] * 1_000_000_000))
                    attempt += 1
                    continue
                }
                return (data, http)
            } catch let error as CloudProError {
                throw error
            } catch {
                throw CloudProError.network(error)
            }
        }
    }

    /// GET with read retries; returns raw body after mapping status errors.
    private func readData(_ request: URLRequest) async throws -> Data {
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
            throw CloudProError.decoding(error)
        }
        if let payload = envelope.data, envelope.success {
            return payload
        }
        throw CloudProError.http(status: 200, body: envelope.error ?? "missing data in response envelope")
    }

    /// Gate layering per the worker: the environment flag (404) sits above the
    /// entitlement (403 upgrade_required).
    private func statusError(_ status: Int, data: Data) -> CloudProError {
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
