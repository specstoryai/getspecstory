import Foundation
import SpecStoryKit

/// One ⌘K result: the merged session row plus its matching context from
/// FTS (local snippet() or the cloud's server-side snippet), highlights
/// marked by STX/ETX spans.
struct SearchResultRow: Identifiable, Equatable {
    let item: SessionItem
    let snippet: HighlightedSnippet?

    var id: String { item.clientID }
}
