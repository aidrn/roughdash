import Foundation

public actor SyncCoordinator {
    private let api: RoughdashAPIClient
    private let catalog: LocalCatalog

    public init(api: RoughdashAPIClient, catalog: LocalCatalog) {
        self.api = api
        self.catalog = catalog
    }

    public func refreshProjectRoots() async throws -> [SyncProject] {
        let projects = try await api.listProjects()
        await catalog.replaceProjects(projects)
        return projects
    }

    public func acquireLease(for device: SyncDevice, force: Bool = false) async throws -> SyncLease {
        guard let deviceID = device.id else {
            throw APIError.invalidResponse
        }
        return try await api.acquireLease(deviceID: deviceID, volumeUUID: device.ssdVolumeUuid, force: force)
    }
}
