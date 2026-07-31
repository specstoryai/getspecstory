import Foundation

/// Splits an arbitrary byte stream into NDJSON lines.
///
/// The chat stream arrives as chunked HTTP bytes with no alignment guarantees:
/// a JSON line can be split across chunks (including mid UTF-8 rune) and the
/// server may close without a trailing newline. Bytes are buffered and only
/// decoded to String per complete line, so multi-byte characters split across
/// chunk boundaries are safe. Callers MUST call flush() at stream end; the
/// VSIX lost the final line by skipping this.
public struct NDJSONLineBuffer {
    private var buffer = Data()

    public init() {}

    /// Appends raw bytes and returns every complete line they finish.
    /// Empty lines (including bare CRLF) are skipped.
    public mutating func append(_ chunk: some DataProtocol) -> [String] {
        buffer.append(contentsOf: chunk)
        var lines: [String] = []
        while let newlineIndex = buffer.firstIndex(of: 0x0A) {
            let lineData = buffer.subdata(in: buffer.startIndex..<newlineIndex)
            buffer.removeSubrange(buffer.startIndex...newlineIndex)
            if let line = Self.decodeLine(lineData) {
                lines.append(line)
            }
        }
        return lines
    }

    /// Returns the trailing partial line at stream end, or nil when the stream
    /// ended cleanly on a newline. Resets the buffer either way.
    public mutating func flush() -> String? {
        defer { buffer.removeAll() }
        return Self.decodeLine(buffer)
    }

    private static func decodeLine(_ data: Data) -> String? {
        var data = data
        // CRLF tolerance: strip one trailing carriage return.
        if data.last == 0x0D {
            data.removeLast()
        }
        guard !data.isEmpty, let line = String(data: data, encoding: .utf8), !line.isEmpty else {
            return nil
        }
        return line
    }
}
