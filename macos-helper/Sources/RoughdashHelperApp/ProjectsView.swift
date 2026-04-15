import RoughdashHelperCore
import SwiftUI

struct ProjectsView: View {
    let projects: [SyncProject]

    var body: some View {
        List(projects) { project in
            VStack(alignment: .leading, spacing: 4) {
                Text(project.name)
                    .font(.headline)
                Text(project.rootPath)
                    .foregroundStyle(.secondary)
                Text(project.ignorePolicy)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .padding(.vertical, 4)
        }
        .navigationTitle("Projects")
    }
}
