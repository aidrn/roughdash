import SwiftUI

struct SetupView: View {
    @Bindable var state: HelperAppState

    var body: some View {
        Form {
            Section("Roughdash Server") {
                TextField("Server URL", text: $state.serverURL)
                TextField("Helper ID", text: $state.helperID)
                SecureField("Helper token", text: $state.helperToken)
            }

            Section("Dedicated SSD") {
                HStack {
                    Text(state.selectedVolumeURL?.path ?? "No volume selected")
                    Spacer()
                    Button("Choose Volume") {
                        chooseVolume()
                    }
                }
                if let check = state.volumeCheck {
                    Label(
                        check.isSupported ? "Ready: \(check.volumeUUID)" : (check.reason ?? "Unsupported volume"),
                        systemImage: check.isSupported ? "checkmark.circle" : "xmark.octagon"
                    )
                    .foregroundStyle(check.isSupported ? .green : .red)
                }
            }

            Section("Status") {
                Text(state.statusMessage)
                    .foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .navigationTitle("Setup")
    }

    private func chooseVolume() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = "Choose SSD"
        if panel.runModal() == .OK, let url = panel.url {
            state.validateVolume(url: url)
        }
    }
}
