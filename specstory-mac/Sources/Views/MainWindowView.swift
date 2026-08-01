import SwiftUI
import SpecStoryKit

/// The Granola-style shell: slim sidebar, content pane, floating Ask bar,
/// search overlay, toasts, and the resume sheet.
struct MainWindowView: View {
    @ObservedObject var model: AppModel

    var body: some View {
        ZStack {
            HStack(spacing: 0) {
                if !model.sidebarCollapsed {
                    SidebarView(model: model)
                    Divider().overlay(Theme.hairline)
                }
                content
            }
            .animation(.easeInOut(duration: 0.18), value: model.sidebarCollapsed)
            .background(Theme.paper)
            .overlay(alignment: .topLeading) {
                if model.sidebarCollapsed {
                    SidebarToggleButton(model: model)
                        .padding(.leading, 74)
                        .padding(.top, 5)
                        .ignoresSafeArea(edges: .top)
                }
            }

            if model.searchOverlayShown {
                SearchOverlay(model: model)
            }

            if let toast = model.toast {
                VStack {
                    Spacer()
                    HStack {
                        Spacer()
                        ToastCard(toast: toast)
                            .padding(.trailing, 20)
                            .padding(.bottom, 20)
                            .transition(.move(edge: .trailing).combined(with: .opacity))
                    }
                }
                .animation(.spring(duration: 0.3), value: model.toast)
                .allowsHitTesting(false)
            }
        }
        .frame(minWidth: 860, minHeight: 560)
        .sheet(item: $model.resumeSheetItem) { item in
            ResumeSheet(model: model, item: item)
        }
        .sheet(isPresented: $model.signInSheetShown) {
            SignInSheet(model: model)
        }
        .sheet(isPresented: $model.resumeUpsellShown) {
            ResumeUpsellSheet(model: model)
        }
        .sheet(item: $model.folderPickItem) { item in
            FolderPickSheet(model: model, item: item)
        }
        .background(KeyboardShortcuts(model: model))
    }

    @ViewBuilder private var content: some View {
        switch model.panelMode {
        case .home:
            ZStack(alignment: .bottom) {
                if let selected = model.selectedSession {
                    SessionDetailView(model: model, item: selected)
                } else {
                    FeedView(model: model)
                    AskBarBackdrop()
                    AskBar(model: model)
                }
            }
        case .chat:
            ChatPanelView(model: model)
        case .skills:
            SkillsView(model: model, pro: model.pro)
        case .analytics:
            AnalyticsView(analytics: model.analytics, pro: model.pro, model: model)
        case .providers:
            ProvidersView(model: model)
        case .settings:
            SettingsView(model: model)
        }
    }
}

/// Invisible helpers for window-level shortcuts.
private struct KeyboardShortcuts: View {
    @ObservedObject var model: AppModel

    var body: some View {
        Group {
            Button("") { model.toggleSearchOverlay() }
                .keyboardShortcut("k", modifiers: .command)
            Button("") {
                // Topmost layer consumes Escape first.
                if model.searchOverlayShown {
                    model.dismissSearchOverlay()
                } else if model.selectedSession != nil {
                    model.closeSession()
                }
            }
            .keyboardShortcut(.escape, modifiers: [])
        }
        .opacity(0)
        .frame(width: 0, height: 0)
        .accessibilityHidden(true)
    }
}

/// Left rail: nav, then account footer. Slim and quiet like Granola.
struct SidebarView: View {
    @ObservedObject var model: AppModel

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            SidebarToggleButton(model: model)
                .padding(.leading, 60)
                .padding(.top, 5)
                .padding(.bottom, 10)

            searchButton
                .padding(.bottom, 10)

            navItem(.home, label: "Home", symbol: "house")
            navItem(.chat, label: "Chat", symbol: "sparkles")
            navItem(.skills, label: "Skills", symbol: "wand.and.stars")
            navItem(.analytics, label: "Analytics", symbol: "chart.bar")
            navItem(.providers, label: "Providers", symbol: "cpu")

            Spacer()

            footer
        }
        .padding([.horizontal, .bottom], 14)
        .frame(width: 216)
        .background(Theme.paper)
        .ignoresSafeArea(edges: .top)
    }

    private var searchButton: some View {
        Button {
            model.toggleSearchOverlay()
        } label: {
            HStack(spacing: 7) {
                Image(systemName: "magnifyingglass")
                    .font(.system(size: 11, weight: .medium))
                Text("Search")
                    .font(Theme.body(12))
                Spacer()
                Text("⌘K")
                    .font(Theme.body(10))
                    .foregroundStyle(Theme.inkTertiary)
            }
            .foregroundStyle(Theme.inkSecondary)
            .padding(.horizontal, 10)
            .padding(.vertical, 7)
            .background(Theme.card, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(Theme.hairline))
        }
        .buttonStyle(.tactile)
    }

    private func navItem(_ mode: PanelMode, label: String, symbol: String) -> some View {
        SidebarNavItem(model: model, mode: mode, label: label, symbol: symbol)
    }

    private var footer: some View {
        VStack(alignment: .leading, spacing: 8) {
            if !model.liveSessions.isEmpty {
                HStack(spacing: 6) {
                    Circle().fill(Theme.live).frame(width: 6, height: 6)
                    Text(model.statusText)
                        .font(Theme.body(11))
                        .foregroundStyle(Theme.inkSecondary)
                }
                .padding(.horizontal, 10)
            }
            AccountFooterView(model: model)
        }
    }
}

/// The account hub anchored at the sidebar's bottom: identity, plan badge,
/// and a popover with plan and account actions (the web app's footer,
/// natively). Local-only concerns stay in the Settings panel.
struct AccountFooterView: View {
    @ObservedObject var model: AppModel
    @ObservedObject var pro: ProModel

    @State private var menuShown = false
    @State private var hovering = false

    init(model: AppModel) {
        self.model = model
        self.pro = model.pro
    }

    var body: some View {
        Button {
            menuShown.toggle()
        } label: {
            HStack(spacing: 8) {
                Text(accountInitial)
                    .font(Theme.display(13))
                    .foregroundStyle(Theme.accent)
                    .frame(width: 26, height: 26)
                    .background(Theme.accent.opacity(0.12), in: Circle())
                VStack(alignment: .leading, spacing: 1) {
                    Text(displayName)
                        .font(Theme.body(11, weight: .medium))
                        .foregroundStyle(Theme.ink)
                        .lineLimit(1)
                    Text(model.signedInEmail == nil ? "Sign in to sync" : "SpecStory Cloud")
                        .font(Theme.body(10))
                        .foregroundStyle(Theme.inkTertiary)
                }
                Spacer()
                planBadge
                Image(systemName: "chevron.up.chevron.down")
                    .font(.system(size: 9))
                    .foregroundStyle(Theme.inkTertiary)
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .background(hovering ? Theme.cardHover : Theme.card, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(Theme.hairline))
            .contentShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
        }
        .buttonStyle(.tactile)
        .onHover { hovering = $0 }
        .popover(isPresented: $menuShown, arrowEdge: .top) {
            AccountMenu(model: model, pro: pro, dismiss: { menuShown = false })
        }
        .accessibilityLabel("Account and plan")
    }

    @ViewBuilder private var planBadge: some View {
        if model.signedInEmail != nil {
            Text(pro.plan.displayName)
                .font(Theme.body(9, weight: .semibold))
                .foregroundStyle(pro.plan == .free ? Theme.inkSecondary : Theme.accent)
                .padding(.horizontal, 6)
                .padding(.vertical, 2)
                .background(
                    (pro.plan == .free ? Theme.sidebarSelection : Theme.accent.opacity(0.12)),
                    in: Capsule()
                )
        }
    }

    private var accountInitial: String {
        guard let email = model.signedInEmail, let first = email.first else { return "?" }
        return String(first).uppercased()
    }

    private var displayName: String {
        guard let email = model.signedInEmail else { return "Not signed in" }
        return String(email.split(separator: "@").first ?? Substring(email))
    }
}

/// The footer popover: plan actions, the web app bridge, settings, session.
private struct AccountMenu: View {
    @ObservedObject var model: AppModel
    @ObservedObject var pro: ProModel
    let dismiss: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            if let email = model.signedInEmail {
                VStack(alignment: .leading, spacing: 2) {
                    Text(email)
                        .font(Theme.body(11, weight: .medium))
                        .foregroundStyle(Theme.ink)
                        .lineLimit(1)
                    Text("\(pro.plan.displayName) plan")
                        .font(Theme.body(10))
                        .foregroundStyle(Theme.inkTertiary)
                }
                .padding(.horizontal, 10)
                .padding(.top, 10)
                .padding(.bottom, 6)
                Divider().overlay(Theme.hairline)
            }

            if model.signedInEmail == nil {
                menuRow("Sign in to SpecStory Cloud", symbol: "person.crop.circle.badge.plus", accent: true) {
                    model.signInSheetShown = true
                }
            } else if pro.plan == .free {
                menuRow("Upgrade to Pro", symbol: "arrow.up.circle", accent: true) {
                    pro.openCheckout()
                }
            } else {
                menuRow("Manage plan and billing", symbol: "creditcard") {
                    pro.openPortal()
                }
            }

            menuRow("Open SpecStory Cloud", symbol: "safari") {
                NSWorkspace.shared.open(AppModel.cloudBaseURL)
            }
            menuRow("App settings", symbol: "gearshape") {
                model.panelMode = .settings
            }

            if model.signedInEmail != nil {
                Divider().overlay(Theme.hairline)
                menuRow("Sign out", symbol: "rectangle.portrait.and.arrow.right") {
                    Task { await model.signOut() }
                }
            }
        }
        .padding(.bottom, 6)
        .frame(width: 230)
    }

    private func menuRow(_ title: String, symbol: String, accent: Bool = false, action: @escaping () -> Void) -> some View {
        MenuRowButton(title: title, symbol: symbol, accent: accent) {
            dismiss()
            action()
        }
    }
}

private struct MenuRowButton: View {
    let title: String
    let symbol: String
    var accent = false
    let action: () -> Void

    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 8) {
                Image(systemName: symbol)
                    .font(.system(size: 11))
                    .frame(width: 16)
                Text(title)
                    .font(Theme.body(12, weight: accent ? .semibold : .regular))
                Spacer()
            }
            .foregroundStyle(accent ? Theme.accent : (hovering ? Theme.ink : Theme.inkSecondary))
            .padding(.horizontal, 10)
            .padding(.vertical, 6)
            .background(hovering ? Theme.sidebarSelection : Color.clear, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            .contentShape(RoundedRectangle(cornerRadius: 6, style: .continuous))
        }
        .buttonStyle(.tactile)
        .onHover { hovering = $0 }
        .padding(.horizontal, 6)
    }
}

/// The tray toggle that collapses and expands the navigation strip.
struct SidebarToggleButton: View {
    @ObservedObject var model: AppModel
    @State private var hovering = false

    var body: some View {
        Button {
            model.sidebarCollapsed.toggle()
        } label: {
            Image(systemName: "sidebar.left")
                .font(.system(size: 13))
                .foregroundStyle(Theme.inkSecondary)
                .frame(width: 28, height: 28)
                .background(hovering ? Theme.sidebarSelection : Color.clear, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
        }
        .buttonStyle(.tactile)
        .onHover { hovering = $0 }
        .help(model.sidebarCollapsed ? "Show sidebar" : "Hide sidebar")
        .accessibilityLabel(model.sidebarCollapsed ? "Show sidebar" : "Hide sidebar")
        .keyboardShortcut("s", modifiers: [.command, .control])
    }
}

/// One sidebar destination. Hovering animates a highlight onto the row;
/// clicking navigates (and closes an open session detail).
struct SidebarNavItem: View {
    @ObservedObject var model: AppModel
    let mode: PanelMode
    let label: String
    let symbol: String

    @State private var hovering = false

    var body: some View {
        let selected = model.panelMode == mode
        Button {
            model.panelMode = mode
            model.closeSession()
        } label: {
            HStack(spacing: 8) {
                Image(systemName: symbol)
                    .font(.system(size: 12, weight: .medium))
                    .frame(width: 16)
                    .scaleEffect(hovering && !selected ? 1.12 : 1)
                Text(label)
                    .font(Theme.body(13, weight: selected ? .semibold : .regular))
                Spacer()
                if mode == .chat && model.askStreaming {
                    ProgressView().controlSize(.mini)
                }
            }
            .foregroundStyle(selected ? Theme.ink : (hovering ? Theme.ink : Theme.inkSecondary))
            .padding(.horizontal, 10)
            .padding(.vertical, 7)
            .background(
                selected ? Theme.sidebarSelection : (hovering ? Theme.sidebarSelection.opacity(0.55) : Color.clear),
                in: RoundedRectangle(cornerRadius: 8, style: .continuous)
            )
            .contentShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
            .offset(x: hovering && !selected ? 2 : 0)
        }
        .buttonStyle(.tactile)
        .onHover { hovering = $0 }
        .animation(.spring(duration: 0.22), value: hovering)
        .accessibilityLabel(label)
    }
}

/// Bottom-right confirmation card: filled icon circle plus the action text.
struct ToastCard: View {
    let toast: AppModel.Toast

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: toast.kind == .success ? "checkmark" : "exclamationmark")
                .font(.system(size: 10, weight: .bold))
                .foregroundStyle(Theme.card)
                .frame(width: 20, height: 20)
                .background(toast.kind == .success ? Theme.inkSecondary : Color.orange, in: Circle())
            Text(toast.message)
                .font(Theme.body(13))
                .foregroundStyle(Theme.ink)
                .lineLimit(2)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .background(Theme.card, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(Theme.hairline))
        .shadow(color: .black.opacity(0.12), radius: 16, y: 5)
        .accessibilityLabel(toast.message)
    }
}
