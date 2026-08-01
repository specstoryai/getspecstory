import SwiftUI
import SpecStoryKit

/// The Granola-style shell: slim sidebar, content pane, floating Ask bar,
/// search overlay, toasts, and the resume sheet.
struct MainWindowView: View {
    @ObservedObject var model: AppModel

    var body: some View {
        ZStack {
            HStack(spacing: 0) {
                if model.sidebarCollapsed {
                    CollapsedSidebarRail(model: model)
                } else {
                    SidebarView(model: model)
                }
                Divider().overlay(Theme.hairline)
                content
            }
            .animation(.easeInOut(duration: 0.18), value: model.sidebarCollapsed)
            .background(Theme.paper)

            if model.searchOverlayShown {
                SearchOverlay(model: model)
            }

            if let toast = model.toast {
                VStack {
                    Spacer()
                    Text(toast)
                        .font(Theme.body(12, weight: .medium))
                        .padding(.horizontal, 14)
                        .padding(.vertical, 8)
                        .background(.regularMaterial, in: Capsule())
                        .overlay(Capsule().strokeBorder(Theme.hairline))
                        .padding(.bottom, 68)
                        .transition(.move(edge: .bottom).combined(with: .opacity))
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
            HStack {
                Spacer()
                SidebarToggleButton(model: model)
            }
            .padding(.bottom, 2)

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
        .padding(14)
        .frame(width: 216)
        .background(Theme.paper)
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
        .buttonStyle(.plain)
    }

    private func navItem(_ mode: PanelMode, label: String, symbol: String) -> some View {
        Button {
            model.panelMode = mode
            model.closeSession()
        } label: {
            HStack(spacing: 8) {
                Image(systemName: symbol)
                    .font(.system(size: 12, weight: .medium))
                    .frame(width: 16)
                Text(label)
                    .font(Theme.body(13, weight: model.panelMode == mode ? .semibold : .regular))
                Spacer()
                if mode == .chat && model.askStreaming {
                    ProgressView().controlSize(.mini)
                }
            }
            .foregroundStyle(model.panelMode == mode ? Theme.ink : Theme.inkSecondary)
            .padding(.horizontal, 10)
            .padding(.vertical, 7)
            .background(
                model.panelMode == mode ? Theme.sidebarSelection : Color.clear,
                in: RoundedRectangle(cornerRadius: 8, style: .continuous)
            )
        }
        .buttonStyle(.plain)
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
            Button {
                if model.signedInEmail == nil {
                    model.signInSheetShown = true
                } else {
                    model.panelMode = .settings
                }
            } label: {
                HStack(spacing: 8) {
                    Image(systemName: model.signedInEmail == nil ? "person.crop.circle.badge.questionmark" : "person.crop.circle")
                        .font(.system(size: 14))
                    VStack(alignment: .leading, spacing: 1) {
                        Text(model.signedInEmail ?? "Not signed in")
                            .font(Theme.body(11, weight: .medium))
                            .lineLimit(1)
                        Text(model.signedInEmail == nil ? "Sign in to sync" : "SpecStory Cloud")
                            .font(Theme.body(10))
                            .foregroundStyle(Theme.inkTertiary)
                    }
                    Spacer()
                    Image(systemName: "gearshape")
                        .font(.system(size: 11))
                        .foregroundStyle(Theme.inkTertiary)
                }
                .foregroundStyle(Theme.inkSecondary)
                .padding(.horizontal, 10)
                .padding(.vertical, 8)
                .background(Theme.card, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(Theme.hairline))
            }
            .buttonStyle(.plain)
        }
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
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(model.sidebarCollapsed ? "Show sidebar" : "Hide sidebar")
        .accessibilityLabel(model.sidebarCollapsed ? "Show sidebar" : "Hide sidebar")
        .keyboardShortcut("s", modifiers: [.command, .control])
    }
}

/// Collapsed state: a slim rail with the toggle up top and the account
/// circle anchored at the bottom.
struct CollapsedSidebarRail: View {
    @ObservedObject var model: AppModel

    var body: some View {
        VStack(spacing: 0) {
            SidebarToggleButton(model: model)
                .padding(.top, 34)

            Spacer()

            Button {
                if model.signedInEmail == nil {
                    model.signInSheetShown = true
                } else {
                    model.panelMode = .settings
                }
            } label: {
                Text(accountInitial)
                    .font(Theme.display(15))
                    .foregroundStyle(Theme.accent)
                    .frame(width: 32, height: 32)
                    .background(Theme.accent.opacity(0.12), in: Circle())
            }
            .buttonStyle(.plain)
            .help(model.signedInEmail ?? "Sign in to SpecStory Cloud")
            .padding(.bottom, 16)
        }
        .frame(width: 52)
        .background(Theme.paper)
    }

    private var accountInitial: String {
        guard let email = model.signedInEmail, let first = email.first else { return "?" }
        return String(first).uppercased()
    }
}
