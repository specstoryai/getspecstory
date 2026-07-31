import AppKit
import Foundation
import SwiftUI
import SpecStoryKit

/// Pro surface state: plan, feature gates, the skills library, and billing
/// actions. Owned by AppModel as a plain stored sub-model (`let pro =
/// ProModel()` in AppModel.swift); views observe it directly so Pro churn
/// does not invalidate the whole god-model.
///
/// Integration wiring (see feature notes):
///   1. AppModel.swift: add `let pro = ProModel()` to the Services section.
///   2. AppModel.bootstrap(): call `pro.configure(auth: auth)` right after
///      `auth.bootstrap()`, and add `Task { await pro.refresh(signedIn: authState != .signedOut) }`
///      next to the existing `Task { await refreshGates() }`.
///   3. AppModel+Auth.swift: in completeSignIn, signOut, and handleAuthChange,
///      add `Task { await pro.refresh(signedIn: authState != .signedOut) }`.
@MainActor
final class ProModel: ObservableObject {
    // MARK: Gate state

    @Published private(set) var plan: Plan = .free
    @Published private(set) var entitlementFeatures: [String: Bool] = [:]
    @Published private(set) var flags: [String: Bool] = [:]
    @Published private(set) var gatesLoaded = false

    // MARK: Skills state

    @Published private(set) var skills: [SkillRow] = []
    @Published private(set) var skillsLoading = false
    @Published private(set) var skillsError: String?
    @Published private(set) var skillsLoadedOnce = false

    // MARK: Billing state

    @Published private(set) var billingBusy = false

    private var api: CloudProAPI?
    private(set) var signedIn = false

    // MARK: Configuration

    /// Wire the API client to the app's AuthManager. Called once from
    /// AppModel.bootstrap(); safe to call again (e.g. in previews).
    func configure(auth: AuthManager) {
        api = CloudProAPI(
            baseURL: AppModel.cloudBaseURL,
            accessTokenProvider: { [auth] in try await auth.validAccessToken() }
        )
    }

    /// Refetch flags and entitlement. Flags fail open; entitlement fails
    /// closed, so any fetch failure reads as the free plan.
    func refresh(signedIn: Bool) async {
        self.signedIn = signedIn
        guard signedIn, let api else {
            plan = .free
            entitlementFeatures = [:]
            flags = [:]
            gatesLoaded = false
            skills = []
            skillsLoadedOnce = false
            skillsError = nil
            return
        }
        flags = await api.flags()
        if let entitlement = try? await api.entitlement() {
            entitlementFeatures = entitlement.features
            plan = Plan(planString: entitlement.plan)
        } else {
            entitlementFeatures = [:]
            plan = .free
        }
        gatesLoaded = true
    }

    // MARK: Gates

    var skillsGate: FeatureGate { gate(for: "skills") }
    var resumeGate: FeatureGate { gate(for: "resume") }
    var analyticsGate: FeatureGate { gate(for: "analytics") }

    private func gate(for feature: String) -> FeatureGate {
        FeatureGate.gate(flagOn: flags[feature], entitled: entitlementFeatures[feature] ?? false)
    }

    // MARK: Skills

    func refreshSkills() async {
        guard let api, !skillsLoading else { return }
        skillsLoading = true
        skillsError = nil
        do {
            skills = try await api.skillsLibrary()
            skillsLoadedOnce = true
        } catch let error as CloudProError {
            switch error {
            case .featureDark:
                // The server says the feature is dark here; fold that into the
                // flag so the gate flips to hidden.
                flags["skills"] = false
                skills = []
            case .upgradeRequired:
                // Entitlement drifted since the last gate fetch; fail closed.
                entitlementFeatures["skills"] = false
                skills = []
            case .unauthorized:
                skills = []
                skillsError = "Sign in to SpecStory Cloud to see your skills."
            default:
                skillsError = error.localizedDescription
            }
        } catch {
            skillsError = error.localizedDescription
        }
        skillsLoading = false
    }

    func copySkillContent(_ skill: SkillRow) {
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(skill.content, forType: .string)
    }

    // MARK: Billing

    static var billingPageURL: URL {
        AppModel.cloudBaseURL.appendingPathComponent("billing")
    }

    /// Opens Stripe Checkout for the plan in the browser. Falls back to the
    /// cloud billing page whenever the checkout endpoint is unavailable
    /// (signed out, billing flag dark, network trouble).
    func openCheckout(plan targetPlan: Plan = .pro) {
        guard !billingBusy else { return }
        billingBusy = true
        Task {
            var destination = Self.billingPageURL
            if let api {
                if let result = try? await api.checkout(plan: targetPlan) {
                    if let url = result.url {
                        destination = url
                    } else if result.alreadySubscribed,
                              let portal = try? await api.billingPortalURL() {
                        destination = portal
                    }
                }
            }
            NSWorkspace.shared.open(destination)
            billingBusy = false
        }
    }

    /// Opens the Stripe Customer Portal, falling back to the cloud billing
    /// page when the user has no billing account yet.
    func openPortal() {
        guard !billingBusy else { return }
        billingBusy = true
        Task {
            var destination = Self.billingPageURL
            if let api, let portal = try? await api.billingPortalURL() {
                destination = portal
            }
            NSWorkspace.shared.open(destination)
            billingBusy = false
        }
    }

    // MARK: Upsell copy

    static let skillsUpsell = UpsellCopy(
        icon: "wand.and.stars",
        title: "Skills come with SpecStory Pro",
        blurb: "SpecStory mines your synced sessions into reusable skills you can install in every coding agent.",
        bullets: [
            "Skill candidates mined from your real sessions",
            "One-click install into Claude Code, Codex, Cursor, and more",
            "Drift badges keep installed skills current",
        ]
    )

    static let resumeUpsell = UpsellCopy(
        icon: "arrow.uturn.forward",
        title: "Cloud resume comes with SpecStory Pro",
        blurb: "Pick up any synced session from any of your machines and relaunch it in your coding agent.",
        bullets: [
            "Resume sessions synced from your other machines",
            "Search every synced session, not just this Mac",
            "Local resume stays free, always",
        ]
    )

    static let analyticsUpsell = UpsellCopy(
        icon: "chart.bar.xaxis",
        title: "Analytics come with SpecStory Pro",
        blurb: "See how you work across agents: activity, time of day, conversation depth, estimated spend, and Spec Score.",
        bullets: [
            "Cross-agent activity and streaks",
            "Estimated spend with cache savings",
            "Spec Score grades your prompts",
        ]
    )
}
