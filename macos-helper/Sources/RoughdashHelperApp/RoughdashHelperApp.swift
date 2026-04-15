import RoughdashHelperCore
import SwiftUI

@main
struct RoughdashHelperApp: App {
    @State private var state = HelperAppState()

    var body: some Scene {
        WindowGroup("Roughdash Helper") {
            ContentView(state: state)
                .frame(minWidth: 900, minHeight: 560)
        }
        .commands {
            CommandMenu("Sync") {
                Button("Refresh Projects") {
                    Task { await state.refreshProjects() }
                }
                .keyboardShortcut("r", modifiers: [.command])
            }
        }

        Settings {
            SettingsView(state: state)
        }
    }
}
