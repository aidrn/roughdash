import RoughdashHelperCore
import SwiftUI

@main
struct RoughdashHelperApp: App {
    @State private var state: HelperAppState

    init() {
        let appState = HelperAppState()
        _state = State(initialValue: appState)
        Task { @MainActor in
            await appState.handleLaunchArguments()
            await appState.refreshFileProviderFromLocalState()
            appState.startAutoRefresh()
        }
    }

    var body: some Scene {
        WindowGroup("Roughdash Helper") {
            ContentView(state: state)
                .frame(minWidth: 900, minHeight: 560)
                .task {
                    await state.handleLaunchArguments()
                    await state.refreshFileProviderFromLocalState()
                    state.startAutoRefresh()
                }
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
