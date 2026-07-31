import SwiftUI
import SpecStoryKit

// The @-mention UI: chip row, typeahead popover, and field wiring helpers.
// Integration pattern (AskBar, SearchOverlay):
//
//   TextField("...", text: $mention.text)
//       .mentionTextFieldSupport(mention, candidates: { candidates })
//       .onSubmit {
//           if mention.consumeReturn(candidates: candidates) { return }
//           submit()
//       }
//       .popover(isPresented: $mention.popoverShown,
//                attachmentAnchor: .rect(.bounds), arrowEdge: .top) {
//           MentionTypeaheadPopover(state: mention, candidates: candidates)
//       }
//   MentionChipRow(state: mention)
//
// where `candidates = mention.candidatesFromApp(cloudProjects: model.cloudProjects)`.

/// Agent identity hue for mention chips and rows; displayName is the mention
/// value, so resolve back through the provider registry.
func mentionAgentColor(_ agentName: String) -> Color {
    Provider.allCases.first { $0.displayName == agentName }?.badgeColor ?? Theme.inkSecondary
}

// MARK: - Chip row

/// Wrapping row of active mention chips: project chips with a folder glyph,
/// agent chips with the provider's identity dot, the time chip with a clock.
/// Quiet capsule style: Theme.card fill, hairline border, 11pt labels, each
/// chip with an x to remove.
struct MentionChipRow: View {
    @ObservedObject var state: MentionState

    var body: some View {
        if state.hasChips {
            ChipWrapLayout(spacing: 6) {
                ForEach(state.chips) { chip in
                    MentionChipView(chip: chip) {
                        state.removeChip(kind: chip.kind, value: chip.value)
                    }
                }
            }
        }
    }
}

private struct MentionChipView: View {
    let chip: MentionItem
    let onRemove: () -> Void

    var body: some View {
        HStack(spacing: 5) {
            MentionKindGlyph(kind: chip.kind, value: chip.value, pointSize: 10)
            Text(chip.label)
                .font(Theme.body(11, weight: .medium))
                .foregroundStyle(Theme.ink)
                .lineLimit(1)
            Button(action: onRemove) {
                Image(systemName: "xmark")
                    .font(.system(size: 8, weight: .semibold))
                    .foregroundStyle(Theme.inkTertiary)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Remove \(chip.label)")
        }
        .padding(.horizontal, 9)
        .padding(.vertical, 4)
        .background(Theme.card, in: Capsule())
        .overlay(Capsule().strokeBorder(Theme.hairline))
    }
}

/// Kind glyph shared by chips and popover rows.
private struct MentionKindGlyph: View {
    let kind: MentionKind
    let value: String
    var pointSize: CGFloat = 11

    var body: some View {
        switch kind {
        case .project:
            Image(systemName: "folder")
                .font(.system(size: pointSize))
                .foregroundStyle(Theme.inkSecondary)
        case .agent:
            Circle()
                .fill(mentionAgentColor(value))
                .frame(width: pointSize * 0.7, height: pointSize * 0.7)
        case .time:
            Image(systemName: "clock")
                .font(.system(size: pointSize))
                .foregroundStyle(Theme.inkSecondary)
        }
    }
}

/// Minimal wrapping HStack for the chip row.
struct ChipWrapLayout: Layout {
    var spacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let maxWidth = proposal.width ?? .infinity
        var x: CGFloat = 0, y: CGFloat = 0, rowHeight: CGFloat = 0, widest: CGFloat = 0
        for subview in subviews {
            let size = subview.sizeThatFits(.unspecified)
            if x > 0, x + spacing + size.width > maxWidth {
                y += rowHeight + spacing
                x = 0
                rowHeight = 0
            }
            if x > 0 { x += spacing }
            x += size.width
            rowHeight = max(rowHeight, size.height)
            widest = max(widest, x)
        }
        return CGSize(width: proposal.width ?? widest, height: subviews.isEmpty ? 0 : y + rowHeight)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x: CGFloat = 0, y: CGFloat = 0, rowHeight: CGFloat = 0
        for subview in subviews {
            let size = subview.sizeThatFits(.unspecified)
            if x > 0, x + spacing + size.width > bounds.width {
                y += rowHeight + spacing
                x = 0
                rowHeight = 0
            }
            if x > 0 { x += spacing }
            subview.place(at: CGPoint(x: bounds.minX + x, y: bounds.minY + y),
                          proposal: ProposedViewSize(size))
            x += size.width
            rowHeight = max(rowHeight, size.height)
        }
    }
}

// MARK: - Typeahead popover

/// Candidate list shown while an @token is active. Attach under the text
/// field via .popover(isPresented: $state.popoverShown, attachmentAnchor:
/// .rect(.bounds), arrowEdge: .top). Click selects; Return selects the
/// highlighted row through MentionState.consumeReturn in the field's onSubmit.
struct MentionTypeaheadPopover: View {
    @ObservedObject var state: MentionState
    let candidates: [MentionItem]

    var body: some View {
        Group {
            if candidates.isEmpty {
                Text("No matches")
                    .font(Theme.body(12))
                    .foregroundStyle(Theme.inkTertiary)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 10)
            } else {
                VStack(alignment: .leading, spacing: 1) {
                    ForEach(Array(candidates.enumerated()), id: \.element.id) { index, item in
                        row(item, highlighted: index == state.highlightedIndex)
                            .onHover { hovering in
                                if hovering { state.highlightedIndex = index }
                            }
                    }
                }
                .padding(4)
            }
        }
        .frame(minWidth: 240, alignment: .leading)
    }

    private func row(_ item: MentionItem, highlighted: Bool) -> some View {
        Button {
            state.select(item)
        } label: {
            HStack(spacing: 8) {
                MentionKindGlyph(kind: item.kind, value: item.value)
                Text(item.label)
                    .font(Theme.body(12))
                    .foregroundStyle(Theme.ink)
                    .lineLimit(1)
                Spacer(minLength: 12)
                Text(item.kind.rawValue)
                    .font(Theme.body(10))
                    .foregroundStyle(Theme.inkTertiary)
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 5)
            .contentShape(RoundedRectangle(cornerRadius: 6, style: .continuous))
            .background(
                highlighted ? Theme.sidebarSelection : .clear,
                in: RoundedRectangle(cornerRadius: 6, style: .continuous)
            )
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(highlighted ? .isSelected : [])
    }
}

// MARK: - Field wiring

/// Shared field wiring: recompute the active token on every edit and drive
/// the popover highlight with the arrow keys while it is open. Return stays
/// in the field's own onSubmit (call MentionState.consumeReturn there first);
/// Escape closes the popover without disturbing outer Esc handling.
struct MentionTextFieldSupport: ViewModifier {
    @ObservedObject var state: MentionState
    var candidates: () -> [MentionItem]

    func body(content: Content) -> some View {
        content
            .onChange(of: state.text) { _ in
                state.textChanged()
            }
            .onMoveCommand { direction in
                guard state.popoverShown else { return }
                switch direction {
                case .down:
                    state.moveHighlight(by: 1, count: candidates().count)
                case .up:
                    state.moveHighlight(by: -1, count: candidates().count)
                default:
                    break
                }
            }
            .onExitCommand {
                if state.popoverShown { state.dismissPopover() }
            }
    }
}

extension View {
    func mentionTextFieldSupport(_ state: MentionState, candidates: @escaping () -> [MentionItem]) -> some View {
        modifier(MentionTextFieldSupport(state: state, candidates: candidates))
    }
}
