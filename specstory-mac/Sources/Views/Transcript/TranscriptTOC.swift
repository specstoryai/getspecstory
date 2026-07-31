import SwiftUI
import SpecStoryKit

/// The numbered exchange jump list: one row per prompt, active row
/// highlighted, click scrolls the transcript.
struct TranscriptTOCList: View {
    let exchanges: [SessionTranscript.Exchange]
    let activeID: Int?
    let onJump: (Int) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("On this page")
                .font(Theme.body(10.5, weight: .semibold))
                .foregroundStyle(Theme.inkTertiary)
                .padding(.horizontal, 14)
                .padding(.top, 14)
                .padding(.bottom, 8)

            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 2) {
                        ForEach(exchanges) { exchange in
                            row(exchange)
                                .id("toc-\(exchange.id)")
                        }
                    }
                    .padding(.horizontal, 8)
                    .padding(.bottom, 10)
                }
                .onChange(of: activeID) { _, newValue in
                    guard let newValue else { return }
                    withAnimation(.easeOut(duration: 0.15)) {
                        proxy.scrollTo("toc-\(newValue)")
                    }
                }
            }

            Divider().overlay(Theme.hairline)

            Text("\(exchanges.count) prompt\(exchanges.count == 1 ? "" : "s")")
                .font(Theme.body(10.5))
                .foregroundStyle(Theme.inkTertiary)
                .padding(.horizontal, 14)
                .padding(.vertical, 9)
        }
    }

    private func row(_ exchange: SessionTranscript.Exchange) -> some View {
        let active = exchange.id == activeID
        return Button {
            onJump(exchange.id)
        } label: {
            HStack(alignment: .top, spacing: 8) {
                ZStack {
                    Circle()
                        .fill(active ? Theme.accent : Theme.sidebarSelection)
                    Text("\(exchange.id + 1)")
                        .font(Theme.body(9, weight: .semibold))
                        .foregroundStyle(active ? Color.white : Theme.inkSecondary)
                        .monospacedDigit()
                }
                .frame(width: 18, height: 18)

                Text(exchange.title)
                    .font(Theme.body(11))
                    .foregroundStyle(active ? Theme.ink : Theme.inkSecondary)
                    .lineLimit(2)
                    .multilineTextAlignment(.leading)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(.horizontal, 6)
            .padding(.vertical, 5)
            .background(
                active ? Theme.accent.opacity(0.10) : Color.clear,
                in: RoundedRectangle(cornerRadius: 7, style: .continuous)
            )
            .contentShape(RoundedRectangle(cornerRadius: 7, style: .continuous))
        }
        .buttonStyle(.plain)
        .help(exchange.title)
    }
}

/// The element filter row: one pill per element kind with counts, the Clean
/// reading preset, and a reset once anything is hidden.
struct ElementFilterRow: View {
    @ObservedObject var state: TranscriptState

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                ForEach(TranscriptElement.allCases) { element in
                    FilterPill(
                        label: element.label,
                        count: state.count(for: element),
                        selected: state.visible.contains(element)
                    ) {
                        state.toggle(element)
                    }
                }

                Divider()
                    .frame(height: 14)
                    .overlay(Theme.hairline)

                FilterPill(
                    label: "Clean reading",
                    count: nil,
                    selected: state.cleanReadingActive
                ) {
                    if state.cleanReadingActive {
                        state.showEverything()
                    } else {
                        state.applyCleanReading()
                    }
                }

                if !state.allVisible, !state.cleanReadingActive {
                    Button("Show everything") {
                        state.showEverything()
                    }
                    .buttonStyle(.plain)
                    .font(Theme.body(11, weight: .medium))
                    .foregroundStyle(Theme.accent)
                }
            }
            .padding(.horizontal, 20)
            .padding(.vertical, 8)
        }
    }
}

struct FilterPill: View {
    let label: String
    let count: Int?
    let selected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 5) {
                Text(label)
                    .font(Theme.body(11, weight: .medium))
                if let count {
                    Text("\(count)")
                        .font(Theme.body(10))
                        .monospacedDigit()
                        .foregroundStyle(selected ? Theme.accent : Theme.inkTertiary)
                }
            }
            .foregroundStyle(selected ? Theme.ink : Theme.inkSecondary)
            .padding(.horizontal, 9)
            .padding(.vertical, 4)
            .background(Capsule().fill(selected ? Theme.accent.opacity(0.12) : Theme.card))
            .overlay(Capsule().strokeBorder(selected ? Theme.accent.opacity(0.35) : Theme.hairline, lineWidth: 1))
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(count.map { "\(label), \($0)" } ?? label)
        .accessibilityAddTraits(selected ? [.isSelected] : [])
    }
}
