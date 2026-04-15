import Foundation
import Observation
import OSLog
import RoughdashHelperCore

@MainActor
@Observable
final class HelperAppState {
    private let logger = Logger(subsystem: "com.roughdash.helper", category: "app-state")
    private var selectedVolumeBookmark: Data?
    private var selectedVolumeSecurityScopeActive = false

    var serverURL = "http://localhost:8420"
    var helperID = ""
    var helperToken = ""
    var selectedVolumeURL: URL?
    var volumeCheck: VolumeCheck?
    var domainIdentifier = ""
    var projects: [SyncProject] = []
    var items: [SyncItem] = []
    var conflicts: [SyncConflict] = []
    var connectionMode: ConnectionMode = .lanDirect
    var statusMessage = "Pair with Roughdash and select a dedicated APFS encrypted SSD."
    var isBusy = false

    init() {
        loadPersistedState()
    }

    private var api: RoughdashAPIClient {
        RoughdashAPIClient(
            baseURL: URL(string: serverURL) ?? URL(string: "http://localhost:8420")!,
            helperID: helperID.isEmpty ? nil : helperID,
            helperToken: helperToken.isEmpty ? nil : helperToken
        )
    }

    func validateVolume(url: URL) {
        do {
            startAccessingSelectedVolume(url)
            selectedVolumeBookmark = try? url.bookmarkData(options: .withSecurityScope, includingResourceValuesForKeys: nil, relativeTo: nil)
            let check = try VolumeValidator().validateDedicatedSSD(at: url)
            selectedVolumeURL = url
            volumeCheck = check
            statusMessage = check.isSupported ? "SSD ready for Roughdash File Provider setup." : (check.reason ?? "Unsupported volume.")
            logger.info("Validated volume \(url.path, privacy: .public), supported: \(check.isSupported, privacy: .public), uuid: \(check.volumeUUID, privacy: .public)")
            persistState()
        } catch {
            volumeCheck = VolumeCheck(url: url, volumeUUID: "", isSupported: false, reason: error.localizedDescription)
            statusMessage = error.localizedDescription
            logger.error("Volume validation failed for \(url.path, privacy: .public): \(error.localizedDescription, privacy: .public)")
        }
    }

    func registerFileProviderDomain() async {
        guard let selectedVolumeURL, let volumeCheck, volumeCheck.isSupported else {
            let path = selectedVolumeURL?.path ?? "none"
            let supported = volumeCheck?.isSupported.description ?? "none"
            let reason = volumeCheck?.reason ?? "none"
            statusMessage = "Choose a supported APFS encrypted external SSD before registering the File Provider domain. path=\(path) supported=\(supported) reason=\(reason)"
            persistState()
            return
        }

        isBusy = true
        defer { isBusy = false }

        do {
            let registration = try await FileProviderDomainRegistrar().registerRoughdashDomain(
                on: selectedVolumeURL,
                volumeUUID: volumeCheck.volumeUUID,
                serverURL: serverURL,
                helperID: helperID
            )
            domainIdentifier = registration.identifier
            persistState()
            if registration.isExternalVolumeDomain {
                statusMessage = "Registered Roughdash File Provider domain on \(selectedVolumeURL.path)."
            } else {
                statusMessage = "Registered Roughdash File Provider domain using standard macOS storage. External-volume File Provider domains returned NSFeatureUnsupportedError on this Mac."
            }
            logger.info("Registered File Provider domain \(registration.identifier, privacy: .public) on \(selectedVolumeURL.path, privacy: .public)")
        } catch {
            statusMessage = Self.describe(error)
            logger.error("File Provider registration failed: \(error.localizedDescription, privacy: .public)")
            persistState()
        }
    }

    func refreshProjects() async {
        isBusy = true
        defer { isBusy = false }
        do {
            let loadedProjects = try await api.listProjects()
            var loadedItems: [SyncItem] = []
            for project in loadedProjects where project.enabled {
                if let rootItems = try? await api.listItems(projectID: project.id) {
                    loadedItems.append(contentsOf: rootItems)
                }
            }
            projects = loadedProjects
            items = loadedItems
            conflicts = try await api.listConflicts()
            persistState()
            statusMessage = "Loaded \(projects.count) project roots and \(conflicts.count) open conflicts."
        } catch {
            statusMessage = error.localizedDescription
        }
    }

    func handleLaunchArguments() async {
        guard CommandLine.arguments.contains("--register-selected-volume") else {
            return
        }
        await registerFileProviderDomain()
    }

    private func loadPersistedState() {
        guard let snapshot = try? HelperStorage().readSnapshot() else {
            return
        }
        serverURL = snapshot.serverURL
        helperID = snapshot.helperID
        domainIdentifier = snapshot.fileProviderDomainIdentifier ?? ""
        projects = snapshot.projects
        items = snapshot.items
        conflicts = snapshot.conflicts
        if let message = snapshot.statusMessage {
            statusMessage = message
        }
        selectedVolumeBookmark = snapshot.selectedVolumeBookmark
        if let url = resolveSelectedVolumeURL(from: snapshot) {
            selectedVolumeURL = url
            if FileManager.default.fileExists(atPath: url.path) {
                volumeCheck = (try? VolumeValidator().validateDedicatedSSD(at: url)) ?? VolumeCheck(
                    url: url,
                    volumeUUID: snapshot.volumeUUID ?? "",
                    isSupported: false,
                    reason: "The selected volume could not be validated."
                )
            } else {
                volumeCheck = VolumeCheck(
                    url: url,
                    volumeUUID: snapshot.volumeUUID ?? "",
                    isSupported: false,
                    reason: "The selected volume is not mounted."
                )
            }
        }
    }

    private func persistState() {
        let snapshot = HelperSnapshot(
                serverURL: serverURL,
                helperID: helperID,
                selectedVolumePath: selectedVolumeURL?.path,
                selectedVolumeBookmark: selectedVolumeBookmark,
                volumeUUID: volumeCheck?.volumeUUID,
                fileProviderDomainIdentifier: domainIdentifier.isEmpty ? nil : domainIdentifier,
            projects: projects,
            items: items,
            conflicts: conflicts,
            statusMessage: statusMessage
        )
        var persistenceErrors: [String] = []

        do {
            let storage = try HelperStorage()
            try storage.writeSnapshot(snapshot)
        } catch {
            persistenceErrors.append(error.localizedDescription)
            logger.error("Could not save App Group helper state: \(error.localizedDescription, privacy: .public)")
        }

        if let selectedVolumeURL {
            do {
                try HelperStorage.writeVolumeMetadata(snapshot, toVolumeAt: selectedVolumeURL)
            } catch {
                persistenceErrors.append(error.localizedDescription)
                logger.error("Could not save SSD helper state: \(error.localizedDescription, privacy: .public)")
            }
        }

        if !persistenceErrors.isEmpty {
            statusMessage = "Could not save Roughdash helper state: \(persistenceErrors.joined(separator: "; "))"
        }
    }

    private func resolveSelectedVolumeURL(from snapshot: HelperSnapshot) -> URL? {
        if let bookmark = snapshot.selectedVolumeBookmark {
            var isStale = false
            if let url = try? URL(
                resolvingBookmarkData: bookmark,
                options: .withSecurityScope,
                relativeTo: nil,
                bookmarkDataIsStale: &isStale
            ) {
                startAccessingSelectedVolume(url)
                return url
            }
        }
        if let path = snapshot.selectedVolumePath {
            return URL(fileURLWithPath: path, isDirectory: true)
        }
        return nil
    }

    private func startAccessingSelectedVolume(_ url: URL) {
        guard !selectedVolumeSecurityScopeActive else {
            return
        }
        selectedVolumeSecurityScopeActive = url.startAccessingSecurityScopedResource()
    }

    private static func describe(_ error: Error) -> String {
        let nsError = error as NSError
        var parts = [
            error.localizedDescription,
            "domain=\(nsError.domain)",
            "code=\(nsError.code)"
        ]
        if !nsError.userInfo.isEmpty {
            let details = nsError.userInfo
                .map { "\($0.key)=\($0.value)" }
                .sorted()
                .joined(separator: ", ")
            parts.append("userInfo={\(details)}")
        }
        return parts.joined(separator: " | ")
    }
}
