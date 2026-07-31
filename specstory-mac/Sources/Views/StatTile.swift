import SwiftUI

/// A quiet headline-number card: big serif value, small uppercase label.
/// Reused across the analytics dashboard; drop into any HStack row.
struct StatTile: View {
    let label: String
    let value: String
    var detail: String? = nil

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(value)
                .font(Theme.display(26))
                .foregroundStyle(Theme.ink)
                .monospacedDigit()
                .lineLimit(1)
                .minimumScaleFactor(0.6)
            Text(label)
                .font(Theme.body(10, weight: .medium))
                .foregroundStyle(Theme.inkTertiary)
                .textCase(.uppercase)
                .kerning(0.4)
            if let detail {
                Text(detail)
                    .font(Theme.body(10))
                    .foregroundStyle(Theme.inkTertiary)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
        .cardChrome()
    }
}
