import Foundation

public struct HelperSnapshot: Codable, Sendable {
    public var serverURL: String
    public var helperID: String
    public var helperToken: String?
    public var selectedVolumePath: String?
    public var selectedVolumeBookmark: Data?
    public var volumeUUID: String?
    public var syncDevice: SyncDevice?
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
        syncDevice: SyncDevice? = nil,
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
        self.syncDevice = syncDevice
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
        let primarySnapshot = try readSnapshot(at: snapshotURL)
        let legacySnapshot = try legacySnapshotURL.flatMap { try readSnapshot(at: $0) }

        if let primarySnapshot, let legacySnapshot {
            return legacySnapshot.updatedAt > primarySnapshot.updatedAt ? legacySnapshot : primarySnapshot
        }

        if let primarySnapshot {
            return primarySnapshot
        }

        if let legacySnapshot {
            try? writeSnapshot(legacySnapshot)
            return legacySnapshot
        }

        return nil
    }

    public func writeSnapshot(_ snapshot: HelperSnapshot) throws {
        let data = try JSONEncoder.roughdash.encode(snapshot)

        switch storageLocation {
        case .appGroup:
            var errors: [String] = []
            var didWrite = false

            do {
                try write(data, to: snapshotURL)
                didWrite = true
            } catch {
                errors.append(Self.describe(error))
            }

            if let legacySnapshotURL {
                do {
                    try write(data, to: legacySnapshotURL)
                    didWrite = true
                } catch {
                    errors.append(Self.describe(error))
                }
            }

            if !didWrite {
                throw HelperStorageWriteError(errors.joined(separator: "; "))
            }

        case .fileProviderStateDirectory:
            try write(data, to: snapshotURL)
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

    private var roughdashDirectory: URL {
        switch storageLocation {
        case .appGroup:
            legacyRoughdashDirectory
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
            nil
        case .fileProviderStateDirectory:
            nil
        }
    }

    private func readSnapshot(at url: URL) throws -> HelperSnapshot? {
        guard FileManager.default.fileExists(atPath: url.path) else {
            return nil
        }
        let data = try Data(contentsOf: url)
        return try JSONDecoder.roughdash.decode(HelperSnapshot.self, from: data)
    }

    private func write(_ data: Data, to url: URL) throws {
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try data.write(to: url, options: .atomic)
    }

    private static func describe(_ error: Error) -> String {
        let nsError = error as NSError
        return "\(error.localizedDescription) (domain=\(nsError.domain), code=\(nsError.code))"
    }
}

private struct HelperStorageWriteError: Error, LocalizedError {
    var message: String

    init(_ message: String) {
        self.message = message
    }

    var errorDescription: String? {
        message
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
