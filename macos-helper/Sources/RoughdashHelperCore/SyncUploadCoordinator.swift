import Foundation
import OSLog

public struct SyncUploadResult: Sendable {
    public var device: SyncDevice
    public var lease: SyncLease
    public var completed: CompletedTransfer
}

public struct SyncDirectoryCreateResult: Sendable {
    public var device: SyncDevice
    public var lease: SyncLease
    public var directory: SyncDirectoryResult
}

public actor SyncUploadCoordinator {
    private let api: RoughdashAPIClient
    private let transferClient: TransferClient
    private let logger = Logger(subsystem: "com.roughdash.helper", category: "sync-upload")

    public init(api: RoughdashAPIClient) {
        self.api = api
        self.transferClient = TransferClient(api: api)
    }

    public func uploadExistingFile(
        snapshot: HelperSnapshot,
        item: SyncItem,
        fileURL: URL,
        baseContentHash: String? = nil,
        forceLease: Bool = false
    ) async throws -> SyncUploadResult {
        guard let volumeUUID = snapshot.volumeUUID, !volumeUUID.isEmpty else {
            throw SyncUploadError.missingVolumeUUID
        }
        guard item.kind == "file", !item.tombstoned else {
            throw SyncUploadError.unsupportedItem
        }

        logger.info("Starting existing-file sync upload for \(item.relativePath, privacy: .public): \(Self.localPathState(fileURL), privacy: .public)")
        let device: SyncDevice
        do {
            device = try await ensureDevice(snapshot: snapshot, volumeUUID: volumeUUID)
        } catch {
            logger.error("Existing-file sync upload failed while ensuring device for \(item.relativePath, privacy: .public): \(Self.describe(error), privacy: .public)")
            throw error
        }
        guard let deviceID = device.id else {
            throw APIError.invalidResponse
        }
        let uploadBase: SyncItem
        do {
            uploadBase = try await uploadBaseItem(for: item, baseContentHash: baseContentHash)
        } catch {
            logger.error("Existing-file sync upload failed while checking current catalog item for \(item.relativePath, privacy: .public): \(Self.describe(error), privacy: .public)")
            throw error
        }
        let lease: SyncLease
        do {
            lease = try await api.acquireLease(deviceID: deviceID, volumeUUID: volumeUUID, force: forceLease)
        } catch {
            logger.error("Existing-file sync upload failed while acquiring lease for \(item.relativePath, privacy: .public): \(Self.describe(error), privacy: .public)")
            throw error
        }
        let completed: CompletedTransfer
        do {
            completed = try await transferClient.uploadFile(
                projectID: uploadBase.projectId,
                deviceID: deviceID,
                itemID: uploadBase.id,
                relativePath: uploadBase.relativePath,
                baseRevision: uploadBase.revision,
                fileURL: fileURL
            )
        } catch {
            logger.error("Existing-file sync upload failed during transfer for \(item.relativePath, privacy: .public): \(Self.describe(error), privacy: .public) | \(Self.localPathState(fileURL), privacy: .public)")
            throw error
        }
        return SyncUploadResult(device: device, lease: lease, completed: completed)
    }

    public func uploadNewFile(
        snapshot: HelperSnapshot,
        projectID: String,
        parentID: String,
        relativePath: String,
        fileURL: URL,
        forceLease: Bool = false
    ) async throws -> SyncUploadResult {
        guard let volumeUUID = snapshot.volumeUUID, !volumeUUID.isEmpty else {
            throw SyncUploadError.missingVolumeUUID
        }
        let name = relativePath.split(separator: "/").last.map(String.init) ?? relativePath
        guard !name.isEmpty else {
            throw SyncUploadError.unsupportedItem
        }

        logger.info("Starting new-file sync upload for \(relativePath, privacy: .public): \(Self.localPathState(fileURL), privacy: .public)")
        let device: SyncDevice
        do {
            device = try await ensureDevice(snapshot: snapshot, volumeUUID: volumeUUID)
        } catch {
            logger.error("New-file sync upload failed while ensuring device for \(relativePath, privacy: .public): \(Self.describe(error), privacy: .public)")
            throw error
        }
        guard let deviceID = device.id else {
            throw APIError.invalidResponse
        }
        let siblings: [SyncItem]
        do {
            siblings = try await api.listItems(projectID: projectID, parentID: parentID)
        } catch {
            logger.error("New-file sync upload failed while listing siblings for \(relativePath, privacy: .public): \(Self.describe(error), privacy: .public)")
            throw error
        }
        if siblings.contains(where: { $0.relativePath == relativePath && !$0.tombstoned }) {
            throw SyncUploadError.itemAlreadyExists
        }
        let lease: SyncLease
        do {
            lease = try await api.acquireLease(deviceID: deviceID, volumeUUID: volumeUUID, force: forceLease)
        } catch {
            logger.error("New-file sync upload failed while acquiring lease for \(relativePath, privacy: .public): \(Self.describe(error), privacy: .public)")
            throw error
        }
        let completed: CompletedTransfer
        do {
            completed = try await transferClient.uploadFile(
                projectID: projectID,
                deviceID: deviceID,
                itemID: nil,
                relativePath: relativePath,
                baseRevision: 0,
                fileURL: fileURL
            )
        } catch {
            logger.error("New-file sync upload failed during transfer for \(relativePath, privacy: .public): \(Self.describe(error), privacy: .public) | \(Self.localPathState(fileURL), privacy: .public)")
            throw error
        }
        return SyncUploadResult(device: device, lease: lease, completed: completed)
    }

    public func createDirectory(
        snapshot: HelperSnapshot,
        projectID: String,
        parentID: String,
        relativePath: String,
        forceLease: Bool = false
    ) async throws -> SyncDirectoryCreateResult {
        guard let volumeUUID = snapshot.volumeUUID, !volumeUUID.isEmpty else {
            throw SyncUploadError.missingVolumeUUID
        }
        let name = relativePath.split(separator: "/").last.map(String.init) ?? relativePath
        guard !name.isEmpty else {
            throw SyncUploadError.unsupportedItem
        }

        let device = try await ensureDevice(snapshot: snapshot, volumeUUID: volumeUUID)
        guard let deviceID = device.id else {
            throw APIError.invalidResponse
        }
        let siblings = try await api.listItems(projectID: projectID, parentID: parentID)
        if siblings.contains(where: { $0.relativePath == relativePath && !$0.tombstoned }) {
            throw SyncUploadError.itemAlreadyExists
        }
        let lease = try await api.acquireLease(deviceID: deviceID, volumeUUID: volumeUUID, force: forceLease)
        let directory = try await api.createDirectory(
            projectID: projectID,
            deviceID: deviceID,
            parentID: parentID,
            relativePath: relativePath,
            baseRevision: 0
        )
        return SyncDirectoryCreateResult(device: device, lease: lease, directory: directory)
    }

    private func ensureDevice(snapshot: HelperSnapshot, volumeUUID: String) async throws -> SyncDevice {
        if let device = snapshot.syncDevice,
           device.ssdVolumeUuid == volumeUUID,
           device.id != nil {
            return device
        }

        let devices = try await api.listDevices()
        if let existing = devices.first(where: { $0.ssdVolumeUuid == volumeUUID }) {
            return existing
        }

        return try await api.registerDevice(SyncDevice(
            machineId: "roughdash-helper-\(snapshot.helperID)",
            name: Host.current().localizedName ?? "Roughdash Mac",
            platform: "darwin",
            ssdVolumeUuid: volumeUUID,
            lastConnectionMode: ConnectionMode.lanDirect.rawValue
        ))
    }

    private func uploadBaseItem(for item: SyncItem, baseContentHash: String?) async throws -> SyncItem {
        guard let current = try await currentCatalogItem(for: item) else {
            return item
        }

        guard current.revision != item.revision else {
            return current
        }

        guard baseContentHash == current.contentHash else {
            return item
        }

        return current
    }

    private func currentCatalogItem(for item: SyncItem) async throws -> SyncItem? {
        let siblings = try await api.listItems(projectID: item.projectId, parentID: item.parentId)
        return siblings.first(where: { candidate in
            candidate.id == item.id || candidate.relativePath == item.relativePath
        })
    }

    private static func localPathState(_ fileURL: URL) -> String {
        let fileManager = FileManager.default
        var isDirectory = ObjCBool(false)
        let exists = fileManager.fileExists(atPath: fileURL.path, isDirectory: &isDirectory)
        let readable = fileManager.isReadableFile(atPath: fileURL.path)
        let writable = fileManager.isWritableFile(atPath: fileURL.path)
        let attributes = try? fileManager.attributesOfItem(atPath: fileURL.path)
        let size = (attributes?[.size] as? NSNumber)?.stringValue ?? "nil"
        let type = (attributes?[.type] as? FileAttributeType)?.rawValue ?? "nil"
        return "path=\(fileURL.path) exists=\(exists) isDirectory=\(isDirectory.boolValue) readable=\(readable) writable=\(writable) size=\(size) type=\(type)"
    }

    private static func describe(_ error: Error) -> String {
        let nsError = error as NSError
        return "\(error.localizedDescription) (domain=\(nsError.domain), code=\(nsError.code))"
    }
}

public enum SyncUploadError: Error, LocalizedError {
    case missingVolumeUUID
    case unsupportedItem
    case itemAlreadyExists

    public var errorDescription: String? {
        switch self {
        case .missingVolumeUUID:
            return "The Roughdash helper does not have a registered SSD volume UUID."
        case .unsupportedItem:
            return "Only file uploads are supported in this build."
        case .itemAlreadyExists:
            return "A sync item already exists at this path."
        }
    }
}
