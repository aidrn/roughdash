import Foundation

public struct HelperSnapshot: Codable, Sendable {
    public var serverURL: String
    public var helperID: String
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

    public init(appGroupIdentifier: String? = nil, bundle: Bundle = .main) throws {
        guard let identifier = appGroupIdentifier ?? Self.appGroupIdentifier(in: bundle), !identifier.isEmpty else {
            throw HelperStorageError.missingAppGroupIdentifier
        }
        guard let container = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: identifier) else {
            throw HelperStorageError.appGroupContainerUnavailable(identifier)
        }
        self.appGroupIdentifier = identifier
        self.containerURL = container
    }

    public static func appGroupIdentifier(in bundle: Bundle = .main) -> String? {
        bundle.object(forInfoDictionaryKey: "RoughdashAppGroupIdentifier") as? String
    }

    public func readSnapshot() throws -> HelperSnapshot? {
        let url = snapshotURL
        guard FileManager.default.fileExists(atPath: url.path) else {
            return nil
        }
        let data = try Data(contentsOf: url)
        return try JSONDecoder.roughdash.decode(HelperSnapshot.self, from: data)
    }

    public func writeSnapshot(_ snapshot: HelperSnapshot) throws {
        try ensureRoughdashDirectory()
        let data = try JSONEncoder.roughdash.encode(snapshot)
        try data.write(to: snapshotURL, options: .atomic)
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

    private var roughdashDirectory: URL {
        containerURL.appendingPathComponent("Roughdash", isDirectory: true)
    }

    private var snapshotURL: URL {
        roughdashDirectory.appendingPathComponent("state.json")
    }

    private func ensureRoughdashDirectory() throws {
        try FileManager.default.createDirectory(at: roughdashDirectory, withIntermediateDirectories: true)
    }
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
