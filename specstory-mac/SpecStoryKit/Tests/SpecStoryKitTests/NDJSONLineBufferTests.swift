import XCTest
@testable import SpecStoryKit

final class NDJSONLineBufferTests: XCTestCase {
    func testMultipleLinesInOneChunk() {
        var buffer = NDJSONLineBuffer()
        let lines = buffer.append(Data("first\nsecond\nthird\n".utf8))
        XCTAssertEqual(lines, ["first", "second", "third"])
        XCTAssertNil(buffer.flush())
    }

    func testLineSplitAcrossChunks() {
        var buffer = NDJSONLineBuffer()
        XCTAssertEqual(buffer.append(Data("{\"da".utf8)), [])
        XCTAssertEqual(buffer.append(Data("ta\":1".utf8)), [])
        XCTAssertEqual(buffer.append(Data("}\n".utf8)), ["{\"data\":1}"])
        XCTAssertNil(buffer.flush())
    }

    func testChunkEndingMidLineThenCompleting() {
        var buffer = NDJSONLineBuffer()
        XCTAssertEqual(buffer.append(Data("alpha\nbet".utf8)), ["alpha"])
        XCTAssertEqual(buffer.append(Data("a\ngam".utf8)), ["beta"])
        XCTAssertEqual(buffer.flush(), "gam")
    }

    func testTrailingLineWithoutNewlineIsFlushed() {
        var buffer = NDJSONLineBuffer()
        XCTAssertEqual(buffer.append(Data("done\ntrailing".utf8)), ["done"])
        XCTAssertEqual(buffer.flush(), "trailing")
        // Flush resets the buffer.
        XCTAssertNil(buffer.flush())
    }

    func testFlushAfterCleanNewlineReturnsNil() {
        var buffer = NDJSONLineBuffer()
        _ = buffer.append(Data("complete\n".utf8))
        XCTAssertNil(buffer.flush())
    }

    func testCRLFTolerance() {
        var buffer = NDJSONLineBuffer()
        let lines = buffer.append(Data("one\r\ntwo\r\n".utf8))
        XCTAssertEqual(lines, ["one", "two"])
    }

    func testCRLFSplitBetweenChunks() {
        var buffer = NDJSONLineBuffer()
        XCTAssertEqual(buffer.append(Data("one\r".utf8)), [])
        XCTAssertEqual(buffer.append(Data("\ntwo".utf8)), ["one"])
        XCTAssertEqual(buffer.flush(), "two")
    }

    func testEmptyLinesSkipped() {
        var buffer = NDJSONLineBuffer()
        let lines = buffer.append(Data("a\n\n\r\nb\n".utf8))
        XCTAssertEqual(lines, ["a", "b"])
        XCTAssertNil(buffer.flush())
    }

    func testTrailingBareCarriageReturnFlushIsNil() {
        var buffer = NDJSONLineBuffer()
        _ = buffer.append(Data("a\n\r".utf8))
        XCTAssertNil(buffer.flush())
    }

    func testMultiByteUTF8SplitAcrossChunks() {
        var buffer = NDJSONLineBuffer()
        let text = "caf\u{00E9} \u{1F600} r\u{00E9}sum\u{00E9}"
        let bytes = Array("\(text)\n".utf8)
        // Split inside the emoji's 4-byte sequence.
        let splitIndex = 8
        XCTAssertEqual(buffer.append(Data(bytes[..<splitIndex])), [])
        XCTAssertEqual(buffer.append(Data(bytes[splitIndex...])), [text])
    }

    func testByteAtATimeDelivery() {
        var buffer = NDJSONLineBuffer()
        var lines: [String] = []
        for byte in Array("ab\ncd".utf8) {
            lines.append(contentsOf: buffer.append(Data([byte])))
        }
        if let tail = buffer.flush() {
            lines.append(tail)
        }
        XCTAssertEqual(lines, ["ab", "cd"])
    }

    func testEmptyAppendProducesNothing() {
        var buffer = NDJSONLineBuffer()
        XCTAssertEqual(buffer.append(Data()), [])
        XCTAssertNil(buffer.flush())
    }
}
