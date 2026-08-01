import XCTest
@testable import SpecStoryKit

/// The MATCH grammar ported from the CLI (session_tui_search.go): these cases
/// mirror its documented behavior so drift is caught here.
final class FTSQueryTests: XCTestCase {
    func testBareWordBecomesPrefixTerm() {
        XCTAssertEqual(SessionIndexReader.ftsQuery(from: "thank"), "thank*")
        XCTAssertEqual(SessionIndexReader.ftsQuery(from: "thank you"), "thank* you*")
    }

    func testPunctuatedWordBecomesAdjacencyChain() {
        XCTAssertEqual(SessionIndexReader.ftsQuery(from: "poem.txt"), "poem + txt*")
        XCTAssertEqual(SessionIndexReader.ftsQuery(from: "auth-race fix"), "auth + race* fix*")
    }

    func testClosedQuoteBecomesExactPhrase() {
        XCTAssertEqual(SessionIndexReader.ftsQuery(from: "\"billing worker\""), "\"billing worker\"")
        XCTAssertEqual(SessionIndexReader.ftsQuery(from: "fix \"auth race\""), "fix* \"auth race\"")
    }

    func testOpenQuoteBecomesPrefixChain() {
        XCTAssertEqual(SessionIndexReader.ftsQuery(from: "\"auth race"), "auth + race*")
    }

    func testPunctuationInsidePhraseTokenizes() {
        XCTAssertEqual(SessionIndexReader.ftsQuery(from: "\"poem.txt file\""), "\"poem txt file\"")
    }

    func testUnderTwoAlphanumericsReturnsNil() {
        XCTAssertNil(SessionIndexReader.ftsQuery(from: "a"))
        XCTAssertNil(SessionIndexReader.ftsQuery(from: "!?"))
        XCTAssertNil(SessionIndexReader.ftsQuery(from: " "))
        XCTAssertNotNil(SessionIndexReader.ftsQuery(from: "ab"))
    }

    func testPurePunctuationWordsDropOut() {
        XCTAssertEqual(SessionIndexReader.ftsQuery(from: "fix -- now"), "fix* now*")
    }

    /// Adversarial inputs that would syntax-error raw FTS5 must all produce
    /// valid expressions (the CLI has no fallback because this cannot fail).
    func testAdversarialInputsProduceValidExpressions() {
        let nasty = ["AND OR NOT", "\"\"\"", "((", "col:val", "a*b", "-x +y", "\"unclosed (paren"]
        for input in nasty {
            let query = SessionIndexReader.ftsQuery(from: input)
            if let query {
                XCTAssertFalse(query.contains("("), "grouping chars must not survive: \(query)")
                XCTAssertFalse(query.contains(":"), "column filters must not survive: \(query)")
            }
        }
    }
}
