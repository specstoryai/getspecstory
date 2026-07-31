import SwiftUI
import SpecStoryKit

/// Guided device-flow sign in: browser shows a code, user pastes it here.
struct SignInSheet: View {
    @ObservedObject var model: AppModel

    @State private var code = ""
    @State private var working = false
    @State private var errorText: String?
    @State private var browserOpened = false
    @FocusState private var codeFocused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            VStack(alignment: .leading, spacing: 4) {
                Text("Connect SpecStory Cloud")
                    .font(Theme.display(22))
                    .foregroundStyle(Theme.ink)
                Text("Sync sessions across machines, search everything, and ask questions grounded in your history.")
                    .font(Theme.body(12))
                    .foregroundStyle(Theme.inkSecondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            VStack(alignment: .leading, spacing: 12) {
                stepRow(number: "1", text: browserOpened
                    ? "A sign-in code is waiting in your browser. Use the copy button next to it."
                    : "Get your sign-in code from cloud.specstory.com.")
                if !browserOpened {
                    Button("Open browser to get code") {
                        model.beginSignIn()
                        browserOpened = true
                        codeFocused = true
                    }
                    .buttonStyle(.borderedProminent)
                    .tint(Theme.accent)
                    .padding(.leading, 30)
                }

                stepRow(number: "2", text: "Paste the code here. Codes are case sensitive and expire quickly.")
                HStack(spacing: 8) {
                    TextField("ABC-123", text: $code)
                        .textFieldStyle(.plain)
                        .font(Theme.mono(20))
                        .multilineTextAlignment(.center)
                        .focused($codeFocused)
                        .frame(width: 150, height: 40)
                        .background(Theme.card, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                        .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous)
                            .strokeBorder(errorText == nil ? Theme.hairline : Theme.live.opacity(0.6)))
                        .onSubmit { submit() }
                        .onChange(of: code) { _ in errorText = nil }
                    Button {
                        submit()
                    } label: {
                        if working {
                            ProgressView().controlSize(.small).frame(width: 60)
                        } else {
                            Text("Connect").frame(width: 60)
                        }
                    }
                    .buttonStyle(.borderedProminent)
                    .tint(Theme.accent)
                    .disabled(working || code.trimmingCharacters(in: .whitespaces).count < 6)
                }
                .padding(.leading, 30)

                if let errorText {
                    VStack(alignment: .leading, spacing: 6) {
                        Label(errorText, systemImage: "exclamationmark.circle")
                            .font(Theme.body(12))
                            .foregroundStyle(Theme.live)
                        Button("Get a fresh code") {
                            code = ""
                            self.errorText = nil
                            model.beginSignIn()
                        }
                        .buttonStyle(.link)
                        .font(Theme.body(12))
                    }
                    .padding(.leading, 30)
                }
            }

            HStack {
                Spacer()
                Button("Not now") {
                    model.signInSheetShown = false
                }
                .keyboardShortcut(.escape, modifiers: [])
            }
        }
        .padding(24)
        .frame(width: 460)
        .onAppear {
            // Open the browser immediately so the code is already on screen
            // by the time the user reads step 2.
            model.beginSignIn()
            browserOpened = true
            codeFocused = true
        }
    }

    private func stepRow(number: String, text: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text(number)
                .font(Theme.body(11, weight: .semibold))
                .foregroundStyle(Theme.inkSecondary)
                .frame(width: 20, height: 20)
                .background(Theme.sidebarSelection, in: Circle())
            Text(text)
                .font(Theme.body(13))
                .foregroundStyle(Theme.ink)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private func submit() {
        let trimmed = code.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, !working else { return }
        working = true
        errorText = nil
        Task {
            do {
                try await model.completeSignIn(code: trimmed)
                model.signInSheetShown = false
            } catch {
                errorText = error.localizedDescription
            }
            working = false
        }
    }
}
