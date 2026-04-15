import Foundation
import Observation
import RoughdashHelperCore

@Observable
final class HelperAppState {
    var serverURL = "http://localhost:8420"
    var helperID = ""
    var helperToken = ""
    var selectedVolumeURL: URL?
    var volumeCheck: VolumeCheck?
    var projects: [SyncProject] = []
    var conflicts: [SyncConflict] = []
    var connectionMode: ConnectionMode = .lanDirect
    var statusMessage = "Pair with Roughdash and select a dedicated APFS encrypted SSD."
    var isBusy = false

    private var api: RoughdashAPIClient {
        RoughdashAPIClient(
            baseURL: URL(string: serverURL) ?? URL(string: "http://localhost:8420")!,
            helperID: helperID.isEmpty ? nil : helperID,
            helperToken: helperToken.isEmpty ? nil : helperToken
        )
    }

    func validateVolume(url: URL) {
        do {
            let check = try VolumeValidator().validateDedicatedSSD(at: url)
            selectedVolumeURL = url
            volumeCheck = check
            statusMessage = check.isSupported ? "SSD ready for Roughdash File Provider setup." : (check.reason ?? "Unsupported volume.")
        } catch {
            volumeCheck = VolumeCheck(url: url, volumeUUID: "", isSupported: false, reason: error.localizedDescription)
            statusMessage = error.localizedDescription
        }
    }

    @MainActor
    func refreshProjects() async {
        isBusy = true
        defer { isBusy = false }
        do {
            projects = try await api.listProjects()
            conflicts = try await api.listConflicts()
            statusMessage = "Loaded \(projects.count) project roots and \(conflicts.count) open conflicts."
        } catch {
            statusMessage = error.localizedDescription
        }
    }
}
