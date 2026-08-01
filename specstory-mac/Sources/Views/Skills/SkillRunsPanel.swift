import SwiftUI
import SpecStoryKit

/// The Run Activity tab: trigger button, live indicator, and the run cards
/// with status, timing, verdict tallies, and expandable shard and dossier
/// detail. The run list is polled every 4 s while any run is in progress.
struct SkillRunsPanel: View {
    @ObservedObject var model: AppModel
    @ObservedObject var pro: ProModel
    let runs: [SkillRun]
    let isPreview: Bool

    @State private var expandedRunIDs: Set<String> = []

    private var anyLive: Bool { runs.contains { $0.isInProgress } }

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 10) {
                headerRow

                if !isPreview, let error = pro.runsError {
                    errorLine(error)
                }

                if !isPreview, pro.runsLoading, runs.isEmpty {
                    HStack {
                        Spacer()
                        ProgressView("Loading runs")
                            .controlSize(.small)
                        Spacer()
                    }
                    .padding(.vertical, 40)
                } else if runs.isEmpty {
                    emptyState
                } else {
                    ForEach(runs) { run in
                        SkillRunCard(
                            run: run,
                            isExpanded: expandedRunIDs.contains(run.id),
                            onToggle: {
                                if expandedRunIDs.contains(run.id) {
                                    expandedRunIDs.remove(run.id)
                                } else {
                                    expandedRunIDs.insert(run.id)
                                }
                            }
                        )
                    }
                }
            }
            .padding(.horizontal, 28)
            .padding(.vertical, 16)
            .frame(maxWidth: Theme.feedWidth)
            .frame(maxWidth: .infinity)
        }
    }

    private var headerRow: some View {
        HStack(alignment: .center, spacing: 12) {
            VStack(alignment: .leading, spacing: 3) {
                Text("Background runs that mine and forge skills from your synced sessions.")
                    .font(Theme.body(12))
                    .foregroundStyle(Theme.inkSecondary)
                if anyLive {
                    HStack(spacing: 5) {
                        Circle()
                            .fill(Theme.accent)
                            .frame(width: 6, height: 6)
                        Text("live")
                            .font(Theme.body(11, weight: .medium))
                            .foregroundStyle(Theme.accent)
                    }
                }
            }

            Spacer()

            Button {
                Task { await pro.triggerSkillsRun() }
            } label: {
                HStack(spacing: 5) {
                    if pro.runTriggerBusy {
                        ProgressView()
                            .controlSize(.small)
                    } else {
                        Image(systemName: "play.circle")
                            .font(.system(size: 12, weight: .medium))
                    }
                    Text(pro.runTriggerBusy ? "Starting…" : "Run now")
                        .font(Theme.body(12, weight: .semibold))
                }
            }
            .buttonStyle(.borderedProminent)
            .tint(Theme.accent)
            .disabled(isPreview || pro.runTriggerBusy || anyLive)
            .help(anyLive ? "A run is already in progress" : "Mine your synced sessions for new skills")
        }
    }

    private func errorLine(_ message: String) -> some View {
        HStack(spacing: 6) {
            Image(systemName: "exclamationmark.triangle")
                .font(.system(size: 10))
            Text(message)
        }
        .font(Theme.body(11.5))
        .foregroundStyle(Theme.live)
    }

    private var emptyState: some View {
        VStack(spacing: 10) {
            Image(systemName: "play.circle")
                .font(.system(size: 28, weight: .light))
                .foregroundStyle(Theme.accent)
            Text("No runs yet")
                .font(Theme.display(19))
                .foregroundStyle(Theme.ink)
            Text("Press Run now to mine your synced sessions for skills. A run with nothing new finishes in seconds.")
                .font(Theme.body(12))
                .foregroundStyle(Theme.inkSecondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 380)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 40)
    }
}

/// One run: status chip, trigger and relative time, live-ticking stats,
/// verdict tallies, and an expandable body listing produced skills and
/// shards.
private struct SkillRunCard: View {
    let run: SkillRun
    let isExpanded: Bool
    let onToggle: () -> Void

    @State private var hovering = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            header

            if run.isInProgress {
                TimelineView(.periodic(from: .now, by: 1)) { context in
                    statsLine(now: context.date)
                }
            } else {
                statsLine(now: Date())
            }

            if isExpanded {
                expandedBody
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardChrome(hovering: hovering)
        .onHover { hovering = $0 }
    }

    private var header: some View {
        Button(action: onToggle) {
            HStack(spacing: 8) {
                SkillsChip(label: run.status.isEmpty ? "unknown" : run.status,
                           color: SkillsStyle.phaseColor(run.phase))

                Text(triggerLine)
                    .font(Theme.body(12))
                    .foregroundStyle(Theme.inkSecondary)

                Spacer()

                HStack(spacing: 5) {
                    ForEach(run.verdictTallies, id: \.verdict) { tally in
                        SkillsChip(label: "\(tally.count) \(tally.verdict)",
                                   color: SkillsStyle.verdictColor(tally.verdict))
                    }
                }

                Image(systemName: isExpanded ? "chevron.down" : "chevron.right")
                    .font(.system(size: 10, weight: .medium))
                    .foregroundStyle(Theme.inkTertiary)
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.tactile)
    }

    private var triggerLine: String {
        let trigger = run.trigger ?? "run"
        let started = run.startedAtDate ?? run.createdAtDate
        if let relative = SkillsStyle.relativeLabel(for: started) {
            return "\(trigger) · \(relative)"
        }
        return trigger
    }

    private func statsLine(now: Date) -> some View {
        HStack(spacing: 14) {
            if let duration = SkillsStyle.durationLabel(run.duration(now: now)) {
                statItem("clock", duration)
            }
            statItem("books.vertical", "\(run.sessionsMined) sessions")
            if run.shardCount > 0 {
                statItem("square.grid.2x2", "\(run.shardsDone)/\(run.shardCount) shards")
            }
            statItem("sparkles", "\(run.dossierTotal) dossiers")
            if let error = run.error, !error.isEmpty {
                HStack(spacing: 4) {
                    Image(systemName: "exclamationmark.triangle")
                        .font(.system(size: 9))
                    Text(error)
                        .lineLimit(1)
                }
                .font(Theme.body(11))
                .foregroundStyle(Theme.live)
            }
        }
    }

    private func statItem(_ symbol: String, _ label: String) -> some View {
        HStack(spacing: 4) {
            Image(systemName: symbol)
                .font(.system(size: 9.5))
            Text(label)
        }
        .font(Theme.body(11))
        .foregroundStyle(Theme.inkTertiary)
    }

    private var expandedBody: some View {
        VStack(alignment: .leading, spacing: 10) {
            Divider().overlay(Theme.hairline)

            if !run.dossiers.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    SkillsSectionCaption(text: "Skills produced")
                    ForEach(Array(run.dossiers.enumerated()), id: \.offset) { _, dossier in
                        dossierRow(dossier)
                    }
                }
            }

            if !run.shards.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    SkillsSectionCaption(text: "Shards")
                    ForEach(Array(run.shards.enumerated()), id: \.offset) { _, shard in
                        shardRow(shard)
                    }
                }
            }

            Text("run \(run.id)")
                .font(Theme.mono(10))
                .foregroundStyle(Theme.inkTertiary)
        }
    }

    private func dossierRow(_ dossier: SkillRunDossier) -> some View {
        HStack(spacing: 7) {
            SkillsChip(label: dossier.verdict ?? "unverified",
                       color: SkillsStyle.verdictColor(dossier.verdict))
            Text(dossier.name)
                .font(Theme.body(11.5, weight: .medium))
                .foregroundStyle(Theme.ink)
                .lineLimit(1)
            if let confidence = dossier.confidence {
                Text("confidence \(confidence.formatted(.number.precision(.fractionLength(2))))")
                    .font(Theme.body(10.5))
                    .foregroundStyle(Theme.inkTertiary)
            }
            if dossier.adjudicated {
                Text("· adjudicated")
                    .font(Theme.body(10.5))
                    .foregroundStyle(Theme.inkTertiary)
            }
            if !dossier.hasSkill {
                Text("· no skill authored")
                    .font(Theme.body(10.5))
                    .foregroundStyle(Theme.inkTertiary)
            }
            Spacer(minLength: 0)
        }
    }

    private func shardRow(_ shard: SkillRunShard) -> some View {
        HStack(spacing: 7) {
            SkillsChip(label: shard.status.isEmpty ? "unknown" : shard.status,
                       color: SkillsStyle.phaseColor(shard.phase))
            Text(shard.scopeLabel)
                .font(Theme.body(11.5))
                .foregroundStyle(Theme.ink)
                .lineLimit(1)
            Text("\(shard.sessionCount) sessions")
                .font(Theme.body(10.5))
                .foregroundStyle(Theme.inkTertiary)
            if let duration = SkillsStyle.durationLabel(shard.duration()) {
                Text(duration)
                    .font(Theme.body(10.5))
                    .foregroundStyle(Theme.inkTertiary)
            }
            if let sandbox = shard.sandboxId, !sandbox.isEmpty {
                HStack(spacing: 3) {
                    Image(systemName: "shippingbox")
                        .font(.system(size: 8.5))
                    Text(sandbox)
                        .font(Theme.mono(10))
                }
                .foregroundStyle(Theme.inkTertiary)
            }
            if let error = shard.error, !error.isEmpty {
                Text(error)
                    .font(Theme.body(10.5))
                    .foregroundStyle(Theme.live)
                    .lineLimit(1)
            }
            Spacer(minLength: 0)
        }
    }
}
