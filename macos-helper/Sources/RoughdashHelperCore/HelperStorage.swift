import Foundation

public struct HelperSnapshot: Codable, Sendable {
    public var serverURL: String
    public var helperID: String
    public var helperToken: String?
    public var selectedVolumePath: String?
    public var selectedVolumeBookmark: Data?
    public var volumeUUID: String?
    public var fileProviderDomainIdentifier: String?
    public var projects: [SyncProject]
    public var items: [SyncItem]
    public var conflicts: [SyncConflict]
    public var statusMessage: String?
    public var updatedAt: Date

    public init(
        serverURL: String,
        helperID: String,
        helperToken: String? = nil,
        selectedVolumePath: String? = nil,
        selectedVolumeBookmark: Data? = nil,
        volumeUUID: String? = nil,
        fileProviderDomainIdentifier: String? = nil,
        projects: [SyncProject] = [],
        items: [SyncItem] = [],
        conflicts: [SyncConflict] = [],
        statusMessage: String? = nil,
        updatedAt: Date = Date()
    ) {
        self.serverURL = serverURL
        self.helperID = helperID
        self.helperToken = helperToken
        self.selectedVolumePath = selectedVolumePath
        self.selectedVolumeBookmark = selectedVolumeBookmark
        self.volumeUUID = volumeUUID
        self.fileProviderDomainIdentifier = fileProviderDomainIdentifier
        self.projects = projects
        self.items = items
        self.conflicts = conflicts
        self.statusMessage = statusMessage
        self.updatedAt = updatedAt
    }
}

public enum HelperStorageError: Error, LocalizedError {
    case missingAppGroupIdentifier
    case appGroupContainerUnavailable(String)

    public var errorDescription: String? {
        switch self {
        case .missingAppGroupIdentifier:
            return "The Roughdash app group identifier is missing from Info.plist."
        case .appGroupContainerUnavailable(let identifier):
            return "The Roughdash app group container is unavailable for \(identifier)."
        }
    }
}

public struct HelperStorage {
    public let appGroupIdentifier: String
    public let containerURL: URL
    private let storageLocation: StorageLocation

    public init(appGroupIdentifier: String? = nil, bundle: Bundle = .main) throws {
        guard let identifier = appGroupIdentifier ?? Self.appGroupIdentifier(in: bundle), !identifier.isEmpty else {
            throw HelperStorageError.missingAppGroupIdentifier
        }
        guard let container = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: identifier) else {
            throw HelperStorageError.appGroupContainerUnavailable(identifier)
        }
        self.appGroupIdentifier = identifier
        self.containerURL = container
        self.storageLocation = .appGroup
    }

    public init(fileProviderStateDirectoryURL: URL) {
        self.appGroupIdentifier = ""
        self.containerURL = fileProviderStateDirectoryURL
        self.storageLocation = .fileProviderStateDirectory
    }

    public static func appGroupIdentifier(in bundle: Bundle = .main) -> String? {
        bundle.object(forInfoDictionaryKey: "RoughdashAppGroupIdentifier") as? String
    }

    public func readSnapshot() throws -> HelperSnapshot? {
        let url = snapshotURL
        if FileManager.default.fileExists(atPath: url.path) {
            let data = try Data(contentsOf: url)
            return try JSONDecoder.roughdash.decode(HelperSnapshot.self, from: data)
        }

        guard let legacySnapshotURL, FileManager.default.fileExists(atPath: legacySnapshotURL.path) else {
            return nil
        }

        let data = try Data(contentsOf: legacySnapshotURL)
        let snapshot = try JSONDecoder.roughdash.decode(HelperSnapshot.self, from: data)
        try? writeSnapshot(snapshot)
        return snapshot
    }

    public func writeSnapshot(_ snapshot: HelperSnapshot) throws {
        try ensureRoughdashDirectory()
        let data = try JSONEncoder.roughdash.encode(snapshot)
        try data.write(to: snapshotURL, options: .atomic)
        if storageLocation == .appGroup {
            try? writeLegacySnapshot(data)
        }
    }

    public func writeVolumeMetadata(_ snapshot: HelperSnapshot, toVolumeAt volumeURL: URL) throws {
        try Self.writeVolumeMetadata(snapshot, toVolumeAt: volumeURL)
    }

    public static func writeVolumeMetadata(_ snapshot: HelperSnapshot, toVolumeAt volumeURL: URL) throws {
        let metadataDirectory = volumeURL.appendingPathComponent(".roughdash", isDirectory: true)
        try FileManager.default.createDirectory(at: metadataDirectory, withIntermediateDirectories: true)
        let data = try JSONEncoder.roughdash.encode(snapshot)
        try data.write(to: metadataDirectory.appendingPathComponent("helper-state.json"), options: .atomic)
    }

    private var fileProviderStorageDirectory: URL {
        containerURL.appendingPathComponent("File Provider Storage", isDirectory: true)
    }

    private var roughdashDirectory: URL {
        switch storageLocation {
        case .appGroup:
            fileProviderStorageDirectory.appendingPathComponent("Roughdash", isDirectory: true)
        case .fileProviderStateDirectory:
            containerURL.appendingPathComponent("Roughdash", isDirectory: true)
        }
    }

    private var legacyRoughdashDirectory: URL {
        containerURL.appendingPathComponent("Roughdash", isDirectory: true)
    }

    private var snapshotURL: URL {
        roughdashDirectory.appendingPathComponent("state.json")
    }

    private var legacySnapshotURL: URL? {
        switch storageLocation {
        case .appGroup:
            legacyRoughdashDirectory.appendingPathComponent("state.json")
        case .fileProviderStateDirectory:
            nil
        }
    }

    private func ensureRoughdashDirectory() throws {
        try FileManager.default.createDirectory(at: roughdashDirectory, withIntermediateDirectories: true)
    }

    private func writeLegacySnapshot(_ data: Data) throws {
        guard let legacySnapshotURL else {
            return
        }
        try FileManager.default.createDirectory(at: legacyRoughdashDirectory, withIntermediateDirectories: true)
        try data.write(to: legacySnapshotURL, options: .atomic)
    }
}

private enum StorageLocation: Equatable, Sendable {
    case appGroup
    case fileProviderStateDirectory
}

private extension JSONEncoder {
    static var roughdash: JSONEncoder {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        return encoder
    }
}

private extension JSONDecoder {
    static var roughdash: JSONDecoder {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return decoder
    }
}
