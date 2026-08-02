import SwiftUI
import SpecStoryKit

/// The expanded agent cockpit inside a live session card: where the agent is
/// working (worktree, branch, model, token usage), what it is doing right
/// now, which files it has touched, an inline diff, and the controls to act
/// on the session. Everything reads quiet; the card stays the hero.
struct CockpitView: View {
    @ObservedObject var cockpit: CockpitModel
    let live: LiveSession
    /// Calls model.revealSession(id:) upstream; passed as a closure so this
    /// file does not depend on AppModel.
    let onViewSession: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Rectangle()
                .fill(Theme.hairline)
                .frame(height: 1)

            if cockpit.loading, cockpit.insight == nil, cockpit.gitState == nil {
                HStack(spacing: 8) {
                    ProgressView()
                        .controlSize(.small)
                    Text("Inspecting session")
                        .font(Theme.body(11))
                        .foregroundStyle(Theme.inkTertiary)
                }
                .padding(.vertical, 4)
            } else {
                headerStrip
                statusLine
                if !touchedFiles.isEmpty || cockpit.gitState != nil {
                    filesRow
                }
                if cockpit.diffLoading || cockpit.diffText != nil {
                    CockpitDiffView(
                        title: cockpit.diffFile.map(CockpitFormat.abbreviatePath) ?? "Full diff",
                        text: cockpit.diffText,
                        loading: cockpit.diffLoading,
                        onClose: { cockpit.closeDiff() }
                    )
                }
            }

            footerControls
        }
    }

    // MARK: Header strip

    private var headerStrip: some View {
        ChipWrapLayout(spacing: 8) {
            pathBadge
            if let branch = branchName {
                branchPill(branch)
            }
            if let model = modelName {
                HStack(spacing: 4) {
                    Image(systemName: "cpu")
                        .font(.system(size: 9))
                    Text(model)
                        .font(Theme.body(11))
                }
                .foregroundStyle(Theme.inkSecondary)
            }
            if let usage = tokenUsage {
                Text(usage)
                    .font(Theme.body(11))
                    .foregroundStyle(Theme.inkTertiary)
                    .monospacedDigit()
            }
        }
    }

    private var pathBadge: some View {
        HStack(spacing: 4) {
            Image(systemName: "folder")
                .font(.system(size: 9))
                .foregroundStyle(Theme.inkTertiary)
            Text(CockpitFormat.abbreviatePath(workingPath))
                .font(Theme.mono(10.5))
                .foregroundStyle(Theme.inkSecondary)
                .lineLimit(1)
                .truncationMode(.head)
            if cockpit.gitState?.isWorktree == true {
                Text("worktree")
                    .font(Theme.body(9, weight: .semibold))
                    .foregroundStyle(Theme.accent)
                    .padding(.horizontal, 5)
                    .padding(.vertical, 1.5)
                    .background(Theme.accent.opacity(0.12), in: Capsule())
            }
        }
        .help(workingPath)
    }

    private func branchPill(_ branch: String) -> some View {
        HStack(spacing: 4) {
            Image(systemName: "arrow.triangle.branch")
                .font(.system(size: 9, weight: .medium))
            Text(branch)
                .font(Theme.mono(10.5))
                .lineLimit(1)
            if let churn = churnLabel {
                Text(churn)
                    .font(Theme.mono(9.5))
                    .foregroundStyle(Theme.inkTertiary)
            }
        }
        .foregroundStyle(Theme.inkSecondary)
        .padding(.horizontal, 8)
        .padding(.vertical, 3)
        .background(Theme.sidebarSelection, in: Capsule())
    }

    // MARK: Status line

    private var statusLine: some View {
        HStack(spacing: 8) {
            HStack(spacing: 6) {
                CockpitPulseDot()
                Text(activityLabel)
                    .font(Theme.body(11))
                    .foregroundStyle(Theme.inkSecondary)
                    .lineLimit(1)
            }
            if cockpit.insight?.waitingForPermission == true {
                HStack(spacing: 4) {
                    Image(systemName: "hand.raised")
                        .font(.system(size: 9, weight: .medium))
                    Text("Waiting for permission")
                        .font(Theme.body(10, weight: .semibold))
                }
                .foregroundStyle(CockpitPalette.amber)
                .padding(.horizontal, 7)
                .padding(.vertical, 2.5)
                .background(CockpitPalette.amber.opacity(0.13), in: Capsule())
            }
            Spacer(minLength: 0)
        }
    }

    // MARK: Touched files

    private var filesRow: some View {
        ChipWrapLayout(spacing: 6) {
            ForEach(touchedFiles, id: \.self) { path in
                CockpitFileChip(
                    label: CockpitFormat.fileName(path),
                    selected: cockpit.diffFile == path && (cockpit.diffText != nil || cockpit.diffLoading),
                    help: path
                ) {
                    cockpit.showDiff(file: path)
                }
            }
            CockpitFileChip(
                label: "Full diff",
                icon: "plus.forwardslash.minus",
                selected: cockpit.diffFile == nil && (cockpit.diffText != nil || cockpit.diffLoading),
                help: "Show the full working diff"
            ) {
                cockpit.showDiff(file: nil)
            }
        }
    }

    // MARK: Footer controls

    private var footerControls: some View {
        HStack(spacing: 8) {
            if cockpit.isPaused(live.projectPath) {
                CockpitActionButton(title: "Resume sync", icon: "play.circle", tint: Theme.synced) {
                    cockpit.resumeSync(projectPath: live.projectPath)
                }
            } else {
                CockpitActionButton(title: "Pause sync", icon: "pause.circle") {
                    cockpit.pauseSync(projectPath: live.projectPath)
                }
            }
            CockpitActionButton(title: "Open in Terminal", icon: "terminal") {
                cockpit.openTerminal(at: cockpit.workingDirectory ?? live.projectPath)
            }
            Spacer()
            CockpitActionButton(title: "View session", icon: "doc.text", tint: Theme.accent) {
                onViewSession()
            }
        }
    }

    // MARK: Derived facts

    /// Written optional-tolerant (?? over optional chaining) so the view
    /// compiles whether the Kit declares these fields String or String?.
    private var workingPath: String {
        let cwd = cockpit.insight?.cwd ?? ""
        return cwd.isEmpty ? live.projectPath : cwd
    }

    private var branchName: String? {
        let fromGit = cockpit.gitState?.branch ?? ""
        if !fromGit.isEmpty { return fromGit }
        let fromInsight = cockpit.insight?.branch ?? ""
        return fromInsight.isEmpty ? nil : fromInsight
    }

    private var modelName: String? {
        let model = cockpit.insight?.model ?? ""
        return model.isEmpty ? nil : model
    }

    private var tokenUsage: String? {
        guard let insight = cockpit.insight else { return nil }
        return CockpitFormat.tokenUsage(
            input: insight.inputTokens,
            output: insight.outputTokens,
            cacheRead: insight.cacheReadTokens
        )
    }

    private var churnLabel: String? {
        guard let git = cockpit.gitState else { return nil }
        let insertions = git.insertions ?? 0
        let deletions = git.deletions ?? 0
        guard insertions + deletions > 0 else { return nil }
        return "+\(insertions) -\(deletions)"
    }

    private var activityLabel: String {
        if let activity = cockpit.insight?.lastActivity, !activity.isEmpty {
            return activity
        }
        return "Session active"
    }

    /// Files the agent touched per the native insight; when the inspector
    /// has none, fall back to the checkout's dirty files so the row is still
    /// useful.
    private var touchedFiles: [String] {
        if let touched = cockpit.insight?.touchedFiles, !touched.isEmpty {
            return touched
        }
        return (cockpit.gitState?.dirtyFiles ?? []).map { $0.path }
    }
}
