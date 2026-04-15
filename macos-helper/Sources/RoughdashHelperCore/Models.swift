import Foundation

public enum ConnectionMode: String, Codable, CaseIterable, Sendable {
    case lanDirect = "LAN direct"
    case tailscaleDirect = "Tailscale direct"
    case peerRelay = "peer relay"
    case derpRelay = "DERP relay"
}

public struct SyncProject: Codable, Identifiable, Sendable {
    public var id: String
    public var name: String
    public var rootPath: String
    public var enabled: Bool
    public var ignorePolicy: String
    public var createdAt: Date
    public var updatedAt: Date
}

public struct SyncDevice: Codable, Identifiable, Sendable {
    public var id: String?
    public var helperId: String?
    public var machineId: String
    public var name: String
    public var platform: String
    public var ssdVolumeUuid: String
    public var lastConnectionMode: String?
    public var pairedAt: Date?
    public var lastSeenAt: Date?

    public init(
        id: String? = nil,
        helperId: String? = nil,
        machineId: String,
        name: String,
        platform: String = "darwin",
        ssdVolumeUuid: String,
        lastConnectionMode: String? = nil,
        pairedAt: Date? = nil,
        lastSeenAt: Date? = nil
    ) {
        self.id = id
        self.helperId = helperId
        self.machineId = machineId
        self.name = name
        self.platform = platform
        self.ssdVolumeUuid = ssdVolumeUuid
        self.lastConnectionMode = lastConnectionMode
        self.pairedAt = pairedAt
        self.lastSeenAt = lastSeenAt
    }
}

public struct SyncLease: Codable, Sendable {
    public var ssdVolumeUuid: String
    public var deviceId: String
    public var token: String
    public var expiresAt: Date
    public var updatedAt: Date
}

public struct SyncItem: Codable, Identifiable, Sendable {
    public var id: String
    public var projectId: String
    public var parentId: String
    public var relativePath: String
    public var name: String
    public var kind: String
    public var size: Int64
    public var modTime: Date?
    public var contentHash: String
    public var metadataHash: String
    public var revision: Int64
    public var tombstoned: Bool
    public var dirty: Bool
    public var createdAt: Date
    public var updatedAt: Date
}

public struct SyncConflict: Codable, Identifiable, Sendable {
    public var id: String
    public var projectId: String
    public var itemId: String
    public var baseRevision: Int64
    public var nasRevision: Int64
    public var ssdRevision: Int64
    public var fields: String
    public var status: String
    public var createdAt: Date
    public var resolvedAt: Date?
}

public struct SyncRevision: Codable, Identifiable, Sendable {
    public var id: String
    public var projectId: String
    public var itemId: String
    public var deviceId: String?
    public var baseRevision: Int64
    public var revision: Int64
    public var operation: String
    public var contentHash: String
    public var metadataHash: String
    public var details: String
    public var createdAt: Date
}

public struct SyncPin: Codable, Identifiable, Sendable {
    public var id: String?
    public var projectId: String
    public var itemId: String
    public var deviceId: String
    public var mode: String
    public var recursive: Bool
    public var createdAt: Date?
}

public struct TransferSession: Codable, Identifiable, Sendable {
    public var id: String?
    public var direction: String
    public var projectId: String
    public var deviceId: String
    public var itemId: String?
    public var relativePath: String
    public var baseRevision: Int64
    public var size: Int64
    public var chunkSize: Int64
    public var sha256: String?
    public var createdAt: Date?
    public var expiresAt: Date?
}

public struct TransferChunkReceipt: Codable, Sendable {
    public var index: Int64
    public var size: Int64
    public var sha256: String
}

public struct CompletedTransfer: Sendable {
    public var transfer: TransferSession
    public var path: String
    public var item: SyncItem
    public var revision: SyncRevision
}

public struct VolumeCheck: Sendable {
    public var url: URL
    public var volumeUUID: String
    public var isSupported: Bool
    public var reason: String?

    public init(url: URL, volumeUUID: String, isSupported: Bool, reason: String? = nil) {
        self.url = url
        self.volumeUUID = volumeUUID
        self.isSupported = isSupported
        self.reason = reason
    }
}
