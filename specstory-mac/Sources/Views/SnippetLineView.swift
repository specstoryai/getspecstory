import SwiftUI
import SpecStoryKit

/// One line of matching context under a search result, highlights bolded in
/// the accent color (the CLI's STX/ETX spans, rendered natively).
struct SnippetLineView: View {
    let snippet: HighlightedSnippet

    var body: some View {
        Text(attributed)
            .font(Theme.body(12.5))
            .foregroundStyle(Theme.inkSecondary)
            .lineLimit(3)
            .lineSpacing(2)
            .multilineTextAlignment(.leading)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 10)
            .padding(.vertical, 7)
            .background(Theme.paper, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: 7, style: .continuous)
                    .strokeBorder(Theme.hairline.opacity(0.6))
            )
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
                piece.font = Theme.body(12.5, weight: .bold)
                piece.foregroundColor = Theme.accent
                piece.backgroundColor = Theme.accent.opacity(0.12)
            }
            result.append(piece)
        }
        return result
    }
}
