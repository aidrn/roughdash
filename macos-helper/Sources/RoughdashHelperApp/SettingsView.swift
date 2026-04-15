import SwiftUI

struct SettingsView: View {
    @Bindable var state: HelperAppState

    var body: some View {
        Form {
            TextField("Server URL", text: $state.serverURL)
            TextField("Helper ID", text: $state.helperID)
            SecureField("Helper token", text: $state.helperToken)
        }
        .padding()
        .frame(width: 460)
    }
}
