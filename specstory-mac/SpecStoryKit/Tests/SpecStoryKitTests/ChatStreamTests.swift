import XCTest
@testable import SpecStoryKit

/// Serves canned HTTP responses, delivering body bytes across multiple
/// didLoad calls to simulate network chunking of the NDJSON stream.
final class ChatStubProtocol: URLProtocol {
    struct Stub {
        var status: Int = 200
        var headers: [String: String] = ["Content-Type": "application/x-ndjson; charset=utf-8"]
        var chunks: [Data] = []
    }

    private static let lock = NSLock()
    private static var stubQueue: [Stub] = []
    private static var seenRequests: [(request: URLRequest, body: Data)] = []

    static func reset(stubs: [Stub]) {
        lock.lock()
        defer { lock.unlock() }
        stubQueue = stubs
        seenRequests = []
    }

    static var requests: [(request: URLRequest, body: Data)] {
        lock.lock()
        defer { lock.unlock() }
        return seenRequests
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let stub: Stub = {
            Self.lock.lock()
            defer { Self.lock.unlock() }
            Self.seenRequests.append((request, Self.bodyData(of: request)))
            return Self.stubQueue.isEmpty ? Stub() : Self.stubQueue.removeFirst()
        }()
        let response = HTTPURLResponse(
            url: request.url!,
            statusCode: stub.status,
            httpVersion: "HTTP/1.1",
            headerFields: stub.headers
        )!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        for chunk in stub.chunks {
            client?.urlProtocol(self, didLoad: chunk)
        }
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}

    // URLSession converts httpBody to a stream before the protocol sees it.
    private static func bodyData(of request: URLRequest) -> Data {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else { return Data() }
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buf = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let count = stream.read(&buf, maxLength: buf.count)
            guard count > 0 else { break }
            data.append(contentsOf: buf[..<count])
        }
        return data
    }
}

final class ChatStreamTests: XCTestCase {
    private func makeClient(token: String? = "test-token") -> ChatStreamClient {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [ChatStubProtocol.self]
        return ChatStreamClient(
            baseURL: URL(string: "https://cloud.example.com/api/v1")!,
            accessTokenProvider: { token },
            session: URLSession(configuration: config)
        )
    }

    private func collect(_ stream: AsyncThrowingStream<ChatStreamEvent, Error>) async throws -> [ChatStreamEvent] {
        var events: [ChatStreamEvent] = []
        for try await event in stream {
            events.append(event)
        }
        return events
    }

    private func askEvents(_ client: ChatStreamClient, query: String = "how did I fix auth?") async throws -> [ChatStreamEvent] {
        try await collect(client.ask(query: query, chatSessionID: nil, projectIDs: nil, timeFilter: nil, agentNames: nil))
    }

    // MARK: ask streaming

    func testAskMapsFullEventSequenceIncludingUnterminatedFinalLine() async throws {
        let lines = [
            #"{"data":{"chatSessionId":"cs1","type":"chat.stream.start","timestamp":"t0"}}"#,
            #"{"data":{"chatSessionId":"cs1","type":"chat.stream.event","timestamp":"t1","response":"Searching all projects..."}}"#,
            #"{"data":{"chatSessionId":"cs1","type":"chat.stream.query_rewritten","timestamp":"t2","response":"Query rewritten.","originalQuery":"how did I fix auth?","rewrittenQuery":"auth token refresh fix"}}"#,
            #"{"data":{"chatSessionId":"cs1","type":"chat.stream.embedding_search_results","timestamp":"t3","response":"Embedding search results.","exchanges":[{"chunk_id":"ch1","exchange_id":"ex1","exchange_chunk_id":"ec1","session_id":"s1","session_client_id":"sc1","project_id":"p1","session_name":"Session One","user_title":"Fix auth","session_summary":"Fixed token refresh","project_name":"getspecstory","project_icon":"FolderKanban","project_color":"blue"}]}}"#,
            #"{"data":{"chatSessionId":"cs1","type":"chat.stream.chunk","timestamp":"t4","response":"You fixed it by "}}"#,
            #"{"data":{"chatSessionId":"cs1","type":"chat.stream.chunk","timestamp":"t5","response":"refreshing early [chunk:ch1]."}}"#,
            #"{"data":{"chatSessionId":"cs1","type":"chat.stream.end","timestamp":"t6"}}"#,
        ]
        // Join with newlines but leave the final line unterminated, then split
        // the byte stream at awkward boundaries (mid-line, mid-JSON).
        let wire = lines.joined(separator: "\n")
        let bytes = Array(wire.utf8)
        let cuts = [50, 51, 200, 490, bytes.count - 10]
        var chunks: [Data] = []
        var previous = 0
        for cut in cuts where cut > previous && cut < bytes.count {
            chunks.append(Data(bytes[previous..<cut]))
            previous = cut
        }
        chunks.append(Data(bytes[previous...]))
        ChatStubProtocol.reset(stubs: [.init(chunks: chunks)])

        let events = try await askEvents(makeClient())

        let expectedSource = ChatSource(
            chunkID: "ch1",
            exchangeID: "ex1",
            exchangeChunkID: "ec1",
            sessionID: "s1",
            sessionClientID: "sc1",
            projectID: "p1",
            sessionName: "Session One",
            userTitle: "Fix auth",
            sessionSummary: "Fixed token refresh",
            projectName: "getspecstory",
            projectIcon: "FolderKanban",
            projectColor: "blue"
        )
        XCTAssertEqual(events, [
            .start(chatSessionID: "cs1"),
            .status("Searching all projects..."),
            .queryRewritten("auth token refresh fix"),
            .sources([expectedSource]),
            .chunk("You fixed it by "),
            .chunk("refreshing early [chunk:ch1]."),
            .end,
        ])
    }

    func testAskSkipsMalformedAndUnknownLines() async throws {
        let wire = [
            "not json at all",
            #"{"data":{"chatSessionId":"cs1","type":"chat.stream.start","timestamp":"t"}}"#,
            #"{"data":{"chatSessionId":"cs1","type":"chat.stream.some_future_type","timestamp":"t","response":"?"}}"#,
            #"{"nodata":true}"#,
            #"{"data":{"chatSessionId":"cs1","type":"chat.stream.chunk","timestamp":"t","response":"hello"}}"#,
            #"{"data":{"chatSessionId":"cs1","type":"chat.stream.end","timestamp":"t"}}"#,
        ].joined(separator: "\n") + "\n"
        ChatStubProtocol.reset(stubs: [.init(chunks: [Data(wire.utf8)])])

        let events = try await askEvents(makeClient())
        XCTAssertEqual(events, [.start(chatSessionID: "cs1"), .chunk("hello"), .end])
    }

    func testAskServerErrorEventBecomesFailureNotThrow() async throws {
        let wire = [
            #"{"data":{"chatSessionId":"cs1","type":"chat.stream.start","timestamp":"t"}}"#,
            #"{"data":{"chatSessionId":"cs1","type":"chat.stream.error","timestamp":"t","response":"Rate limit exceeded"}}"#,
        ].joined(separator: "\n")
        ChatStubProtocol.reset(stubs: [.init(chunks: [Data(wire.utf8)])])

        let events = try await askEvents(makeClient())
        XCTAssertEqual(events, [.start(chatSessionID: "cs1"), .failure("Rate limit exceeded")])
    }

    func testAskThrowsOnHTTPError() async {
        ChatStubProtocol.reset(stubs: [.init(status: 401, chunks: [Data("unauthorized".utf8)])])
        do {
            _ = try await askEvents(makeClient())
            XCTFail("Expected throw")
        } catch let error as ChatStreamError {
            XCTAssertEqual(error, .http(status: 401, body: "unauthorized"))
        } catch {
            XCTFail("Unexpected error: \(error)")
        }
    }

    func testAskThrowsSignedOutWhenTokenMissing() async {
        ChatStubProtocol.reset(stubs: [])
        do {
            _ = try await askEvents(makeClient(token: nil))
            XCTFail("Expected throw")
        } catch let error as ChatStreamError {
            XCTAssertEqual(error, .signedOut)
        } catch {
            XCTFail("Unexpected error: \(error)")
        }
        XCTAssertTrue(ChatStubProtocol.requests.isEmpty)
    }

    func testAskSendsExpectedRequest() async throws {
        let wire = #"{"data":{"chatSessionId":"cs9","type":"chat.stream.end","timestamp":"t"}}"# + "\n"
        ChatStubProtocol.reset(stubs: [.init(chunks: [Data(wire.utf8)])])

        _ = try await collect(makeClient().ask(
            query: "what changed?",
            chatSessionID: "cs9",
            projectIDs: ["p1", "p2"],
            timeFilter: "week",
            agentNames: ["cursor"]
        ))

        let recorded = ChatStubProtocol.requests
        XCTAssertEqual(recorded.count, 1)
        let request = recorded[0].request
        XCTAssertEqual(request.httpMethod, "POST")
        XCTAssertEqual(request.url?.path, "/api/v1/chat")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer test-token")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Content-Type"), "application/json")

        let body = try XCTUnwrap(JSONSerialization.jsonObject(with: recorded[0].body) as? [String: Any])
        XCTAssertEqual(body["query"] as? String, "what changed?")
        XCTAssertEqual(body["stream"] as? Bool, true)
        XCTAssertEqual(body["chatSessionId"] as? String, "cs9")
        XCTAssertEqual(body["projectIds"] as? [String], ["p1", "p2"])
        XCTAssertEqual(body["timeFilter"] as? String, "week")
        XCTAssertEqual(body["agentNames"] as? [String], ["cursor"])
    }

    // MARK: thread endpoints

    func testChatsDecodesEnvelopeAndSendsLimit() async throws {
        let json = #"{"success":true,"data":{"chatSessions":[{"id":"c1","title":"Auth fixes","created_at":"2026-07-30T10:00:00Z","updated_at":"2026-07-30T11:00:00Z","messages_count":4},{"id":"c2","title":null}]}}"#
        ChatStubProtocol.reset(stubs: [.init(chunks: [Data(json.utf8)])])

        let chats = try await makeClient().chats(limit: 25)

        XCTAssertEqual(chats.count, 2)
        XCTAssertEqual(chats[0].id, "c1")
        XCTAssertEqual(chats[0].title, "Auth fixes")
        XCTAssertEqual(chats[0].messageCount, 4)
        XCTAssertNil(chats[1].title)
        let request = ChatStubProtocol.requests[0].request
        XCTAssertEqual(request.url?.path, "/api/v1/chats")
        XCTAssertEqual(request.url?.query, "limit=25")
    }

    func testReadsRetryOn5xxThenSucceed() async throws {
        let json = #"{"success":true,"data":{"chatSessions":[]}}"#
        ChatStubProtocol.reset(stubs: [
            .init(status: 503, chunks: [Data("oops".utf8)]),
            .init(chunks: [Data(json.utf8)]),
        ])

        let chats = try await makeClient().chats(limit: 10)
        XCTAssertEqual(chats, [])
        XCTAssertEqual(ChatStubProtocol.requests.count, 2)
    }

    func testReadsGiveUpAfterTwoRetries() async {
        ChatStubProtocol.reset(stubs: [
            .init(status: 429, chunks: []),
            .init(status: 429, chunks: []),
            .init(status: 429, chunks: [Data("slow down".utf8)]),
        ])
        do {
            _ = try await makeClient().chats(limit: 10)
            XCTFail("Expected throw")
        } catch let error as ChatStreamError {
            XCTAssertEqual(error, .http(status: 429, body: "slow down"))
        } catch {
            XCTFail("Unexpected error: \(error)")
        }
        XCTAssertEqual(ChatStubProtocol.requests.count, 3)
    }

    func testChatDetailDecodesMessagesAndSourceFallback() async throws {
        // History rows have exchange_chunk_id but no chunk_id; chunkID must
        // fall back so citations remain resolvable.
        let json = """
        {"success":true,"data":{"chatSession":{
          "id":"c1","title":"Auth fixes","created_at":"2026-07-30T10:00:00Z","updated_at":"2026-07-30T11:00:00Z",
          "messages":[{
            "id":"m1","rag_chat_session_id":"c1","query":"how?","response":"Like this [chunk:ec1].",
            "created_at":"2026-07-30T10:01:00Z","metadata":{},
            "message_chunks":[{
              "id":"mc1","exchange_chunk_id":"ec1","exchange_id":"ex1","session_id":"s1",
              "session_client_id":"sc1","project_id":"p1","session_name":"Session One",
              "user_title":null,"session_summary":"Summary","project_name":"proj",
              "project_icon":"FolderKanban","project_color":"blue","relevance_score":0.9
            }]
          }]
        }}}
        """
        ChatStubProtocol.reset(stubs: [.init(chunks: [Data(json.utf8)])])

        let thread = try await makeClient().chat(id: "c1")

        XCTAssertEqual(thread.id, "c1")
        XCTAssertEqual(thread.messages.count, 1)
        let message = thread.messages[0]
        XCTAssertEqual(message.query, "how?")
        XCTAssertEqual(message.response, "Like this [chunk:ec1].")
        XCTAssertEqual(message.sources.count, 1)
        XCTAssertEqual(message.sources[0].chunkID, "ec1")
        XCTAssertEqual(message.sources[0].exchangeChunkID, "ec1")
        XCTAssertEqual(message.sources[0].sessionClientID, "sc1")
        XCTAssertNil(message.sources[0].userTitle)
        XCTAssertEqual(ChatStubProtocol.requests[0].request.url?.path, "/api/v1/chat/c1")
    }

    func testDeleteChatUsesDeleteMethod() async throws {
        let json = #"{"success":true,"data":{"message":"Chat session deleted successfully"}}"#
        ChatStubProtocol.reset(stubs: [.init(chunks: [Data(json.utf8)])])

        try await makeClient().deleteChat(id: "c1")

        let request = ChatStubProtocol.requests[0].request
        XCTAssertEqual(request.httpMethod, "DELETE")
        XCTAssertEqual(request.url?.path, "/api/v1/chat/c1")
    }

    func testChunkDetailDecodesNestedMetadata() async throws {
        let json = """
        {"success":true,"data":{
          "chunk_id":"ec1","exchange_id":"ex1","project_id":"p1","session_id":"s1",
          "content":"the actual exchange text","relevance_score":0.0,
          "project_name":"proj","project_icon":"FolderKanban","project_color":"gray",
          "conversation_file":"",
          "metadata":{"conversation_title":"Fix auth","workspace_name":"proj"}
        }}
        """
        ChatStubProtocol.reset(stubs: [.init(chunks: [Data(json.utf8)])])

        let detail = try await makeClient().chunk(id: "ec1")

        XCTAssertEqual(detail.chunkID, "ec1")
        XCTAssertEqual(detail.content, "the actual exchange text")
        XCTAssertEqual(detail.projectName, "proj")
        XCTAssertEqual(detail.conversationTitle, "Fix auth")
        XCTAssertEqual(detail.workspaceName, "proj")
        XCTAssertEqual(ChatStubProtocol.requests[0].request.url?.path, "/api/v1/chunks/ec1")
    }
}
