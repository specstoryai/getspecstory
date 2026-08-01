import AppKit
import Foundation
import SpecStoryKit

extension AppModel {
    // MARK: Sign in / out (device flow)

    /// Opens the browser page that shows the 6-character device code.
    func beginSignIn() {
        NSWorkspace.shared.open(AppModel.cloudBaseURL.appendingPathComponent("cli-login"))
    }

    func completeSignIn(code: String) async throws {
        let email = try await auth.signIn(deviceCode: code)
        authState = auth.state
        showToast("Signed in as \(email)")
        supervisor?.restartAll()
        await refreshGates()
        Task { await pro.refresh(signedIn: true) }
        Task { await refreshChatThreads() }
        await refreshFeed()
    }

    func signOut() async {
        await auth.signOut()
        authState = auth.state
        currentEntitlement = nil
        currentFlags = [:]
        Task { await pro.refresh(signedIn: false) }
        supervisor?.restartAll()
        await refreshFeed()
    }

    func handleAuthChange(_ state: AuthState) {
        let wasSignedIn = authState != .signedOut
        authState = state
        if case .signedOut = state, wasSignedIn {
            // Entitlements fail closed: a signed-out identity keeps no gates.
            currentEntitlement = nil
            currentFlags = [:]
            Task { await pro.refresh(signedIn: false) }
            if notificationsEnabled {
                NotificationService.shared.notifySignInNeeded()
            }
            supervisor?.restartAll()
            Task { await refreshFeed() }
        }
    }

    var signedInEmail: String? {
        if case .signedIn(let email) = authState { return email }
        return nil
    }
}
