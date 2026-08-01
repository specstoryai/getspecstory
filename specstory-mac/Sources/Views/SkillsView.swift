import SwiftUI
import SpecStoryKit

/// The Skills panel: the full cloud Skills workspace (library plus run
/// activity), wrapped in the Granola-style Pro gate. When the gate is not
/// enabled the same panel renders sample fixtures blurred under the upgrade
/// card so free users see the shape of the feature.
struct SkillsView: View {
    @ObservedObject var model: AppModel
    @ObservedObject var pro: ProModel

    var body: some View {
        ProGateView(
            gate: pro.skillsGate,
            featureBlurb: "Skills distilled from your sessions are part of SpecStory Pro.",
            planName: "Pro",
            planPrice: "$25/mo",
            onUpgrade: { pro.openCheckout() },
            onManagePlan: { pro.openPortal() }
        ) {
            SkillsWorkspacePanel(model: model, pro: pro, isPreview: pro.skillsGate != .enabled)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Theme.paper)
    }
}

/// The workspace itself: header, Library and Run Activity tabs. `isPreview`
/// swaps live state for the static sample fixtures and disables actions
/// (the gated underlay).
private struct SkillsWorkspacePanel: View {
    @ObservedObject var model: AppModel
    @ObservedObject var pro: ProModel
    let isPreview: Bool

    private enum Tab {
        case library
        case activity
    }

    @State private var tab: Tab = .library

    private var skills: [SkillRow] { isPreview ? ProModel.sampleSkills : pro.skills }
    private var runs: [SkillRun] { isPreview ? ProModel.sampleRuns : pro.skillRuns }

    var body: some View {
        VStack(spacing: 0) {
            header
            tabBar
            Divider().overlay(Theme.hairline)

            switch tab {
            case .library:
                SkillLibraryPanel(model: model, pro: pro, skills: skills, isPreview: isPreview)
            case .activity:
                SkillRunsPanel(model: model, pro: pro, runs: runs, isPreview: isPreview)
            }
        }
        .task(id: pro.skillsGate) {
            guard pro.skillsGate == .enabled else { return }
            if !pro.skillsLoadedOnce { await pro.refreshSkills() }
            if !pro.runsLoadedOnce { await pro.refreshRuns() }
        }
        .onAppear {
            // Rearm the 4 s live poll if a run was in flight when the panel
            // was last closed.
            if pro.skillsGate == .enabled, pro.runsLoadedOnce {
                Task { await pro.refreshRuns(quiet: true) }
            }
        }
        .onDisappear { pro.stopRunPolling() }
        .sheet(item: $pro.selectedSkill) { skill in
            SkillDetailSheet(model: model, pro: pro, skill: skill)
        }
    }

    private var header: some View {
        HStack(spacing: 10) {
            Text("Skills")
                .font(Theme.display(24))
                .foregroundStyle(Theme.ink)
            if pro.gatesLoaded {
                Text(pro.plan.displayName)
                    .font(Theme.body(10, weight: .semibold))
                    .foregroundStyle(Theme.accent)
                    .padding(.horizontal, 7)
                    .padding(.vertical, 3)
                    .background(Theme.accent.opacity(0.12), in: Capsule())
            }
            Spacer()
            if !isPreview {
                if pro.skillsLoading || pro.runsLoading {
                    ProgressView().controlSize(.small)
                } else {
                    Button {
                        Task {
                            await pro.refreshSkills()
                            await pro.refreshRuns()
                        }
                    } label: {
                        Image(systemName: "arrow.clockwise")
                            .font(.system(size: 12, weight: .medium))
                            .foregroundStyle(Theme.inkSecondary)
                    }
                    .buttonStyle(.plain)
                    .help("Refresh skills and runs")
                }
            }
        }
        .padding(.horizontal, 28)
        .padding(.top, 24)
        .padding(.bottom, 10)
    }

    private var tabBar: some View {
        HStack(spacing: 20) {
            tabButton("Library", target: .library, live: false)
            tabButton("Run Activity", target: .activity, live: runs.contains { $0.isInProgress })
            Spacer()
        }
        .padding(.horizontal, 28)
    }

    private func tabButton(_ label: String, target: Tab, live: Bool) -> some View {
        let active = tab == target
        return Button {
            tab = target
        } label: {
            VStack(spacing: 6) {
                HStack(spacing: 5) {
                    Text(label)
                        .font(Theme.body(12.5, weight: active ? .semibold : .regular))
                        .foregroundStyle(active ? Theme.ink : Theme.inkSecondary)
                    if live {
                        Circle()
                            .fill(Theme.accent)
                            .frame(width: 6, height: 6)
                    }
                }
                Rectangle()
                    .fill(active ? Theme.accent : Color.clear)
                    .frame(height: 2)
            }
            .fixedSize(horizontal: true, vertical: false)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}
