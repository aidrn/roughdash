import RoughdashHelperCore
import SwiftUI

struct ContentView: View {
    @Bindable var state: HelperAppState
    @SceneStorage("selection") private var selection: SidebarSelection = .setup

    var body: some View {
        NavigationSplitView {
            SidebarView(selection: $selection, conflictCount: state.visibleConflicts.count)
        } detail: {
            switch selection {
            case .setup:
                SetupView(state: state)
            case .projects:
                ProjectsView(projects: state.projects)
            case .conflicts:
                ConflictsView(conflicts: state.visibleConflicts, items: state.items, projects: state.projects)
            case .transfers:
                TransfersView(connectionMode: state.connectionMode, statusMessage: state.statusMessage)
            }
        }
        .toolbar {
            Button("Refresh") {
                Task { await state.refreshProjects() }
            }
            .disabled(state.isBusy)
        }
    }
}

enum SidebarSelection: String, Codable, Hashable {
    case setup
    case projects
    case conflicts
    case transfers
}
