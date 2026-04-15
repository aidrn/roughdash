import SwiftUI

struct SidebarView: View {
    @Binding var selection: SidebarSelection
    let conflictCount: Int

    var body: some View {
        List(selection: $selection) {
            Label("Setup", systemImage: "externaldrive.badge.checkmark")
                .tag(SidebarSelection.setup)
            Label("Projects", systemImage: "folder")
                .tag(SidebarSelection.projects)
            Label("Conflicts \(conflictCount)", systemImage: "exclamationmark.triangle")
                .tag(SidebarSelection.conflicts)
            Label("Transfers", systemImage: "arrow.left.arrow.right")
                .tag(SidebarSelection.transfers)
        }
        .listStyle(.sidebar)
        .navigationTitle("Roughdash")
    }
}
