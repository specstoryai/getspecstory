import Foundation
import XCTest

/// URLProtocol stub for CloudAPI and AuthManager tests. Responses are served
/// FIFO from `queue`; every request is recorded for assertions. XCTest runs
/// these suites serially in one process, so static state is safe.
final class StubURLProtocol: URLProtocol {
    struct Stub {
        var status: Int = 200
        var headers: [String: String] = [:]
        var body: Data = Data()
        var error: Error?
        var delay: TimeInterval = 0
    }

    struct Recorded {
        let url: URL
        let method: String
        let headers: [String: String]
        let body: Data?

        func header(_ name: String) -> String? {
            headers.first { $0.key.caseInsensitiveCompare(name) == .orderedSame }?.value
        }

        var bodyJSON: [String: Any]? {
            guard let body else { return nil }
            return (try? JSONSerialization.jsonObject(with: body)) as? [String: Any]
        }
    }

    private static let lock = NSLock()
    private static var queue: [Stub] = []
    private static var recorded: [Recorded] = []

    static func reset() {
        lock.lock()
        queue = []
        recorded = []
        lock.unlock()
    }

    static func enqueue(status: Int = 200, headers: [String: String] = [:], json: String = "", delay: TimeInterval = 0) {
        lock.lock()
        queue.append(Stub(status: status, headers: headers, body: Data(json.utf8), delay: delay))
        lock.unlock()
    }

    static func enqueueError(_ error: Error) {
        lock.lock()
        queue.append(Stub(error: error))
        lock.unlock()
    }

    static var requests: [Recorded] {
        lock.lock()
        defer { lock.unlock() }
        return recorded
    }

    static func makeConfiguration() -> URLSessionConfiguration {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [StubURLProtocol.self]
        configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
        configuration.urlCache = nil
        return configuration
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let body = Self.bodyData(of: request)
        Self.lock.lock()
        Self.recorded.append(Recorded(
            url: request.url ?? URL(fileURLWithPath: "/"),
            method: request.httpMethod ?? "GET",
            headers: request.allHTTPHeaderFields ?? [:],
            body: body
        ))
        let stub = Self.queue.isEmpty ? Stub(status: 599, body: Data("no stub queued".utf8)) : Self.queue.removeFirst()
        Self.lock.unlock()

        if stub.delay > 0 {
            Thread.sleep(forTimeInterval: stub.delay)
        }
        if let error = stub.error {
            client?.urlProtocol(self, didFailWithError: error)
            return
        }
        let response = HTTPURLResponse(
            url: request.url ?? URL(fileURLWithPath: "/"),
            statusCode: stub.status,
            httpVersion: "HTTP/1.1",
            headerFields: stub.headers
        )!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: stub.body)
        client?.urlProtocolDidFinishLoading(self)
    }

    // URLSession turns httpBody into a stream by the time it reaches URLProtocol.
    private static func bodyData(of request: URLRequest) -> Data? {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else { return nil }
        stream.open()
        defer { stream.close() }
        var data = Data()
        let size = 4096
        let buffer = UnsafeMutablePointer<UInt8>.allocate(capacity: size)
        defer { buffer.deallocate() }
        while stream.hasBytesAvailable {
            let read = stream.read(buffer, maxLength: size)
            if read <= 0 { break }
            data.append(buffer, count: read)
        }
        return data
    }
}
