import SwiftUI
import SpecStoryKit

/// One line of matching context under a search result, highlights bolded in
/// the accent color (the CLI's STX/ETX spans, rendered natively).
struct SnippetLineView: View {
    let snippet: HighlightedSnippet

    var body: some View {
        Text(attributed)
            .font(Theme.body(11))
            .foregroundStyle(Theme.inkSecondary)
            .lineLimit(2)
            .multilineTextAlignment(.leading)
    }

    private var attributed: AttributedString {
        var result = AttributedString()
        for span in snippet.spans {
            // Snippets come from raw transcript text; collapse whitespace.
            let cleaned = span.text
                .components(separatedBy: .whitespacesAndNewlines)
                .filter { !$0.isEmpty }
                .joined(separator: " ")
            guard !cleaned.isEmpty else { continue }
            var piece = AttributedString(cleaned + " ")
            if span.isHighlighted {
                piece.font = Theme.body(11, weight: .bold)
                piece.foregroundColor = Theme.accent
            }
            result.append(piece)
        }
        return result
    }
}
