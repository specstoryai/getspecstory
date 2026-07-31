import Foundation
import UserNotifications

/// UNUserNotificationCenter wrapper. Authorization is requested lazily before
/// the first delivery; a denial is remembered so we never re-prompt and later
/// notify calls surface nothing. Tapping a session notification (banner body
/// or its "Open" action) routes the session id through `onOpenSession`.
final class NotificationService: NSObject, UNUserNotificationCenterDelegate {
    static let shared = NotificationService()

    /// Set by the app model; called on the main queue.
    var onOpenSession: ((_ sessionID: String) -> Void)?

    static let sessionStartedCategoryID = "SESSION_STARTED"
    static let openSessionActionID = "OPEN_SESSION"
    static let sessionIDUserInfoKey = "sessionID"

    private let center = UNUserNotificationCenter.current()

    private enum AuthorizationState {
        case undetermined
        case granted
        case denied
    }

    // Only touched on the main queue (ensureAuthorized hops back to main).
    private var authorizationState: AuthorizationState = .undetermined

    private override init() {
        super.init()
        center.delegate = self
        registerCategories()
    }

    // MARK: - Posting

    func notifyNewSession(sessionID: String, providerName: String, projectName: String) {
        let content = UNMutableNotificationContent()
        content.title = "New \(providerName) session"
        content.body = projectName
        content.sound = .default
        content.categoryIdentifier = Self.sessionStartedCategoryID
        content.userInfo = [Self.sessionIDUserInfoKey: sessionID]
        deliver(content, identifier: "session-started-\(sessionID)")
    }

    func notifySyncError(message: String) {
        let content = UNMutableNotificationContent()
        content.title = "SpecStory sync problem"
        content.body = message
        content.sound = .default
        // Stable identifier so repeated errors replace rather than stack.
        deliver(content, identifier: "sync-error")
    }

    func notifySignInNeeded() {
        let content = UNMutableNotificationContent()
        content.title = "Sign in to SpecStory"
        content.body = "Your session has expired. Sign in again to keep syncing to SpecStory Cloud."
        content.sound = .default
        // Stable identifier so repeated prompts replace rather than stack.
        deliver(content, identifier: "sign-in-needed")
    }

    // MARK: - Delegate

    /// Show banners even while the app is frontmost.
    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        completionHandler([.banner, .sound])
    }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        defer { completionHandler() }
        // Both the explicit Open action and the default banner tap open the session.
        let action = response.actionIdentifier
        guard action == Self.openSessionActionID || action == UNNotificationDefaultActionIdentifier else {
            return
        }
        guard let sessionID = response.notification.request.content
            .userInfo[Self.sessionIDUserInfoKey] as? String
        else {
            return
        }
        DispatchQueue.main.async { [weak self] in
            self?.onOpenSession?(sessionID)
        }
    }

    // MARK: - Internals

    private func registerCategories() {
        let open = UNNotificationAction(
            identifier: Self.openSessionActionID,
            title: "Open",
            options: [.foreground]
        )
        let sessionStarted = UNNotificationCategory(
            identifier: Self.sessionStartedCategoryID,
            actions: [open],
            intentIdentifiers: [],
            options: []
        )
        center.setNotificationCategories([sessionStarted])
    }

    private func deliver(_ content: UNMutableNotificationContent, identifier: String) {
        ensureAuthorized { [weak self] granted in
            guard let self, granted else { return }
            let request = UNNotificationRequest(identifier: identifier, content: content, trigger: nil)
            self.center.add(request, withCompletionHandler: nil)
        }
    }

    /// Resolves authorization exactly once per state transition: undetermined
    /// prompts, denied stays silent forever, granted delivers. Completion runs
    /// on the main queue.
    private func ensureAuthorized(_ completion: @escaping (Bool) -> Void) {
        let finish: (Bool) -> Void = { granted in
            DispatchQueue.main.async { completion(granted) }
        }
        let resolve: (AuthorizationState) -> Void = { [weak self] state in
            DispatchQueue.main.async {
                self?.authorizationState = state
                completion(state == .granted)
            }
        }

        // Fast path when we already know the answer (main-queue callers).
        if Thread.isMainThread {
            switch authorizationState {
            case .granted:
                completion(true)
                return
            case .denied:
                completion(false)
                return
            case .undetermined:
                break
            }
        }

        center.getNotificationSettings { [weak self] settings in
            guard let self else {
                finish(false)
                return
            }
            switch settings.authorizationStatus {
            case .authorized, .provisional:
                resolve(.granted)
            case .denied:
                // Remember the denial; never re-prompt, never surface errors.
                resolve(.denied)
            default:
                self.center.requestAuthorization(options: [.alert, .sound]) { granted, _ in
                    resolve(granted ? .granted : .denied)
                }
            }
        }
    }
}
