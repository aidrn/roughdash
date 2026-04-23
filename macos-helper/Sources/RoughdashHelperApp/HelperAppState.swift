import FileProvider
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
    private var launchArgumentsHandled = false
    private var autoRefreshTask: Task<Void, Never>?

    var serverURL = "http://localhost:8420"
    var helperID = ""
    var helperToken = ""
    var selectedVolumeURL: URL?
    var volumeCheck: VolumeCheck?
    var domainIdentifier = ""
    var projects: [SyncProject] = []
    var items: [SyncItem] = []
    var conflicts: [SyncConflict] = []
    var externalVolumeProbeReport: FileProviderExternalVolumeProbeReport?
    var syncDevice: SyncDevice?
    var connectionMode: ConnectionMode = .lanDirect
    var statusMessage = "Pair with Roughdash and select a dedicated APFS encrypted SSD."
    var isBusy = false
    var visibleConflicts: [SyncConflict] {
        var seen = Set<String>()
        return conflicts.filter { conflict in
            let key = [
                conflict.projectId,
                conflict.itemId,
                String(conflict.baseRevision),
                String(conflict.nasRevision),
                String(conflict.ssdRevision),
                conflict.fields,
                conflict.status
            ].joined(separator: ":")
            return seen.insert(key).inserted
        }
    }

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
            if registration.isExternalVolumeDomain {
                statusMessage = "Registered Roughdash File Provider domain on \(selectedVolumeURL.path)."
            } else {
                statusMessage = "Registered Roughdash File Provider domain using standard macOS storage. External-volume File Provider domains returned NSFeatureUnsupportedError on this Mac."
            }
            do {
                try await refreshCatalogFromServer()
            } catch {
                logger.error("Could not refresh Roughdash catalog after File Provider registration: \(error.localizedDescription, privacy: .public)")
                statusMessage += " Catalog refresh failed: \(error.localizedDescription)"
            }
            persistState()
            await notifyFileProviderCatalogChanged()
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
            try await refreshCatalogFromServer()
            persistState()
            await notifyFileProviderCatalogChanged()
            statusMessage = "Loaded \(projects.count) project roots and \(conflicts.count) open conflicts."
        } catch {
            statusMessage = error.localizedDescription
        }
    }

    func handleLaunchArguments() async {
        guard !launchArgumentsHandled else {
            return
        }
        launchArgumentsHandled = true

        let arguments = CommandLine.arguments
        if arguments.contains("--reset-file-provider-domain") {
            await resetFileProviderDomain()
        }
        if arguments.contains("--register-selected-volume") {
            await registerFileProviderDomain()
        }
        if arguments.contains("--probe-selected-volume") {
            await runExternalVolumeProbe()
        }
    }

    func refreshFileProviderFromLocalState() async {
        persistState()
        await notifyFileProviderCatalogChanged()
    }

    func startAutoRefresh() {
        guard autoRefreshTask == nil else {
            return
        }
        autoRefreshTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 15 * 1_000_000_000)
                await self?.autoRefreshOnce()
            }
        }
    }

    private func loadPersistedState() {
        guard let snapshot = try? HelperStorage().readSnapshot() else {
            return
        }
        serverURL = snapshot.serverURL
        helperID = snapshot.helperID
        helperToken = snapshot.helperToken ?? ""
        domainIdentifier = snapshot.fileProviderDomainIdentifier ?? ""
        projects = snapshot.projects
        items = snapshot.items
        conflicts = snapshot.conflicts
        syncDevice = snapshot.syncDevice
        if let message = snapshot.statusMessage {
            statusMessage = message
        }
        externalVolumeProbeReport = try? HelperStorage().readExternalVolumeProbeReport()
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
            helperToken: helperToken.isEmpty ? nil : helperToken,
            selectedVolumePath: selectedVolumeURL?.path,
            selectedVolumeBookmark: selectedVolumeBookmark,
            volumeUUID: volumeCheck?.volumeUUID,
            syncDevice: syncDevice,
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

        do {
            try writeFileProviderStateSnapshot(snapshot)
        } catch {
            persistenceErrors.append(error.localizedDescription)
            logger.error("Could not save File Provider state snapshot: \(Self.describe(error), privacy: .public)")
        }

        if !persistenceErrors.isEmpty {
            statusMessage = "Could not save Roughdash helper state: \(persistenceErrors.joined(separator: "; "))"
        }
    }

    private func refreshCatalogFromServer() async throws {
        let loadedProjects = try await api.listProjects()
        var loadedItems: [SyncItem] = []
        for project in loadedProjects where project.enabled {
            if let scan = try? await api.scanProject(projectID: project.id) {
                loadedItems.append(contentsOf: scan.items)
            } else if let allItems = try? await api.listItems(projectID: project.id, recursive: true) {
                loadedItems.append(contentsOf: allItems)
            } else if let rootItems = try? await api.listItems(projectID: project.id) {
                loadedItems.append(contentsOf: rootItems)
            }
        }
        projects = loadedProjects
        items = loadedItems
        conflicts = try await api.listConflicts()
        if let volumeUUID = volumeCheck?.volumeUUID {
            syncDevice = try await api.listDevices().first(where: { $0.ssdVolumeUuid == volumeUUID })
        }
    }

    func runExternalVolumeProbe() async {
        isBusy = true
        defer { isBusy = false }

        let report = await FileProviderExternalVolumeProbe().run(on: selectedVolumeURL)
        externalVolumeProbeReport = report
        statusMessage = report.summary
        persistState()

        do {
            let storage = try HelperStorage()
            try storage.writeExternalVolumeProbeReport(report)
            logger.info("Saved external-volume probe report to \(storage.externalVolumeProbeReportURL.path, privacy: .public)")
        } catch {
            statusMessage += " Report save failed: \(Self.describe(error))"
            logger.error("Could not save external-volume probe report: \(Self.describe(error), privacy: .public)")
        }
    }

    private func autoRefreshOnce() async {
        guard !serverURL.isEmpty,
              !helperID.isEmpty,
              !helperToken.isEmpty,
              !domainIdentifier.isEmpty,
              !isBusy else {
            return
        }

        do {
            try await refreshCatalogFromServer()
            persistState()
            await notifyFileProviderCatalogChanged()
            statusMessage = "Auto-updated \(projects.count) project roots, \(items.filter { !$0.tombstoned }.count) items, and \(visibleConflicts.count) open conflicts."
        } catch {
            logger.error("Auto refresh failed: \(Self.describe(error), privacy: .public)")
        }
    }

    private func writeFileProviderStateSnapshot(_ snapshot: HelperSnapshot) throws {
        guard !domainIdentifier.isEmpty else {
            return
        }

        guard #available(macOS 15.0, *) else {
            return
        }

        guard let manager = NSFileProviderManager(for: fileProviderDomain()) else {
            logger.error("Could not create File Provider manager for state snapshot domain \(self.domainIdentifier, privacy: .public)")
            return
        }

        let stateDirectoryURL = try manager.stateDirectoryURL()
        let didAccessStateDirectory = stateDirectoryURL.startAccessingSecurityScopedResource()
        defer {
            if didAccessStateDirectory {
                stateDirectoryURL.stopAccessingSecurityScopedResource()
            }
        }

        let storage = HelperStorage(fileProviderStateDirectoryURL: stateDirectoryURL)
        try storage.writeSnapshot(snapshot)
        logger.info("Saved File Provider state snapshot to \(stateDirectoryURL.path, privacy: .public)")
    }

    private func resetFileProviderDomain() async {
        isBusy = true
        defer { isBusy = false }

        do {
            let removedCount = try await FileProviderDomainRegistrar().removeRoughdashDomains(volumeUUID: volumeCheck?.volumeUUID)
            domainIdentifier = ""
            statusMessage = "Removed \(removedCount) Roughdash File Provider domain(s)."
            persistState()
            logger.info("Removed \(removedCount, privacy: .public) Roughdash File Provider domain(s)")
        } catch {
            statusMessage = Self.describe(error)
            persistState()
            logger.error("Could not remove Roughdash File Provider domain: \(error.localizedDescription, privacy: .public)")
        }
    }

    private func notifyFileProviderCatalogChanged() async {
        guard !domainIdentifier.isEmpty else {
            return
        }

        do {
            let domain = fileProviderDomain()
            guard let manager = NSFileProviderManager(for: domain) else {
                logger.error("Could not create File Provider manager for domain \(self.domainIdentifier, privacy: .public)")
                return
            }

            try await signalEnumerator(manager)
            await reimportProjectRoots(manager)
            logger.info("Requested File Provider refresh for domain \(self.domainIdentifier, privacy: .public)")
        } catch {
            logger.error("Could not refresh File Provider domain \(self.domainIdentifier, privacy: .public): \(error.localizedDescription, privacy: .public)")
        }
    }

    private func fileProviderDomain() -> NSFileProviderDomain {
        NSFileProviderDomain(
            identifier: NSFileProviderDomainIdentifier(domainIdentifier),
            displayName: "Roughdash"
        )
    }

    private func signalEnumerator(_ manager: NSFileProviderManager) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            manager.signalEnumerator(for: .workingSet) { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume()
                }
            }
        }
    }

    private func reimportProjectRoots(_ manager: NSFileProviderManager) async {
        for project in projects where project.enabled {
            let identifier = RoughdashFileProviderIdentifiers.projectRootIdentifier(projectID: project.id)
            do {
                try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
                    manager.reimportItems(below: identifier) { error in
                        if let error {
                            continuation.resume(throwing: error)
                        } else {
                            continuation.resume()
                        }
                    }
                }
            } catch {
                logger.error("Could not reimport File Provider project root \(project.id, privacy: .public): \(Self.describe(error), privacy: .public)")
            }
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
