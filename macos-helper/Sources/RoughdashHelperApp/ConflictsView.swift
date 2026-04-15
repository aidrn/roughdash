import RoughdashHelperCore
import SwiftUI

struct ConflictsView: View {
    let conflicts: [SyncConflict]
    let items: [SyncItem]
    let projects: [SyncProject]

    var body: some View {
        List(conflicts) { conflict in
            VStack(alignment: .leading, spacing: 4) {
                Text(title(for: conflict))
                    .font(.headline)
                Text("\(conflict.fields): NAS revision \(conflict.nasRevision), SSD revision \(conflict.ssdRevision)")
                    .foregroundStyle(.secondary)
                Text(conflict.createdAt.formatted())
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .padding(.vertical, 4)
        }
        .navigationTitle("Conflicts")
    }

    private func title(for conflict: SyncConflict) -> String {
        guard let item = items.first(where: { $0.id == conflict.itemId }) else {
            return "Unknown sync item"
        }
        let projectName = projects.first(where: { $0.id == conflict.projectId })?.name
        if let projectName {
            return "\(projectName)/\(item.relativePath)"
        }
        return item.relativePath
    }
}
