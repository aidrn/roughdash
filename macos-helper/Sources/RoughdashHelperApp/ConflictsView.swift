import RoughdashHelperCore
import SwiftUI

struct ConflictsView: View {
    let conflicts: [SyncConflict]

    var body: some View {
        List(conflicts) { conflict in
            VStack(alignment: .leading, spacing: 4) {
                Text(conflict.fields)
                    .font(.headline)
                Text("NAS revision \(conflict.nasRevision), SSD revision \(conflict.ssdRevision)")
                    .foregroundStyle(.secondary)
                Text(conflict.createdAt.formatted())
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .padding(.vertical, 4)
        }
        .navigationTitle("Conflicts")
    }
}
