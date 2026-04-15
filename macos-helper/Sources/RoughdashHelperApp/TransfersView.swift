import RoughdashHelperCore
import SwiftUI

struct TransfersView: View {
    let connectionMode: ConnectionMode
    let statusMessage: String

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Label(connectionMode.rawValue, systemImage: "network")
                .font(.title2)
            Text(statusMessage)
                .foregroundStyle(.secondary)
            Text("Transfer path priority: LAN direct, Tailscale direct, peer relay, DERP relay.")
                .foregroundStyle(.secondary)
            Spacer()
        }
        .padding()
        .navigationTitle("Transfers")
    }
}
