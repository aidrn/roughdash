import CryptoKit
import FileProvider
import Foundation
import OSLog
import RoughdashHelperCore

final class RoughdashFileProviderExtension: NSObject, NSFileProviderReplicatedExtension, @unchecked Sendable {
    private let logger = Logger(subsystem: "com.roughdash.helper", category: "file-provider")
    private let domain: NSFileProviderDomain
    private let catalog = LocalCatalog()

    required init(domain: NSFileProviderDomain) {
        self.domain = domain
        super.init()
    }

    func invalidate() {
        // File Provider calls this when the domain is removed or the extension is torn down.
    }

    func item(
        for identifier: NSFileProviderItemIdentifier,
        request: NSFileProviderRequest,
        completionHandler: @escaping (NSFileProviderItem?, Error?) -> Void
    ) -> Progress {
        let progress = Progress(totalUnitCount: 1)
        let snapshot = RoughdashExtensionStorage.readSnapshot(for: domain)

        if identifier == .rootContainer {
            completionHandler(RoughdashRootProviderItem(domain: domain), nil)
        } else if identifier == .trashContainer {
            completionHandler(RoughdashTrashProviderItem(), nil)
        } else if let projectID = RoughdashFileProviderIdentifiers.projectID(from: identifier),
                  let project = snapshot.projects.first(where: { $0.id == projectID && $0.enabled }) {
            completionHandler(RoughdashProjectProviderItem(project: project), nil)
        } else if let item = snapshot.items.first(where: { $0.id == identifier.rawValue && !$0.tombstoned }) {
            completionHandler(RoughdashProviderItem(item: item), nil)
        } else {
            logger.error("No File Provider item found for \(identifier.rawValue, privacy: .public)")
            completionHandler(nil, NSFileProviderError(.noSuchItem))
        }

        progress.completedUnitCount = 1
        return progress
    }

    func fetchContents(
        for itemIdentifier: NSFileProviderItemIdentifier,
        version requestedVersion: NSFileProviderItemVersion?,
        request: NSFileProviderRequest,
        completionHandler: @escaping (URL?, NSFileProviderItem?, Error?) -> Void
    ) -> Progress {
        let progress = Progress(totalUnitCount: 1)
        let fetch = FileProviderContentFetch(progress: progress, completionHandler: completionHandler)
        let itemID = itemIdentifier.rawValue

        Task {
            do {
                let snapshot = RoughdashExtensionStorage.readSnapshot(for: domain)
                guard let item = snapshot.items.first(where: { $0.id == itemID && !$0.tombstoned }) else {
                    throw NSFileProviderError(.noSuchItem)
                }
                guard item.kind == "file" else {
                    throw NSFileProviderError(.noSuchItem)
                }
                guard let serverURL = URL(string: snapshot.serverURL),
                      !snapshot.helperID.isEmpty,
                      let helperToken = snapshot.helperToken,
                      !helperToken.isEmpty else {
                    throw NSFileProviderError(.notAuthenticated)
                }

                let api = RoughdashAPIClient(baseURL: serverURL, helperID: snapshot.helperID, helperToken: helperToken)
                let content = try await api.downloadItemContent(projectID: item.projectId, itemID: item.id)
                try Self.verify(content: content.data, expectedHash: item.contentHash)
                let fileURL = try writeTemporaryContent(content.data, for: item)

                logger.info("Hydrated File Provider item \(item.id, privacy: .public) to \(fileURL.path, privacy: .public)")
                fetch.complete(fileURL, RoughdashProviderItem(item: item), nil)
            } catch {
                logger.error("Could not hydrate File Provider item \(itemID, privacy: .public): \(error.localizedDescription, privacy: .public)")
                fetch.complete(nil, nil, error)
            }
        }

        return progress
    }

    func createItem(
        basedOn itemTemplate: NSFileProviderItem,
        fields: NSFileProviderItemFields,
        contents url: URL?,
        options: NSFileProviderCreateItemOptions,
        request: NSFileProviderRequest,
        completionHandler: @escaping (NSFileProviderItem?, NSFileProviderItemFields, Bool, Error?) -> Void
    ) -> Progress {
        let progress = Progress(totalUnitCount: 1)
        // Local creates are uploaded only after a lease is held and the NAS base revision is checked.
        completionHandler(nil, fields, false, NSFileProviderError(.serverUnreachable))
        progress.completedUnitCount = 1
        return progress
    }

    func modifyItem(
        _ item: NSFileProviderItem,
        baseVersion version: NSFileProviderItemVersion,
        changedFields: NSFileProviderItemFields,
        contents newContents: URL?,
        options: NSFileProviderModifyItemOptions,
        request: NSFileProviderRequest,
        completionHandler: @escaping (NSFileProviderItem?, NSFileProviderItemFields, Bool, Error?) -> Void
    ) -> Progress {
        let progress = Progress(totalUnitCount: 1)
        // The real implementation marks the item dirty locally, then uploads or creates a conflict.
        completionHandler(nil, changedFields, false, NSFileProviderError(.serverUnreachable))
        progress.completedUnitCount = 1
        return progress
    }

    func deleteItem(
        identifier: NSFileProviderItemIdentifier,
        baseVersion version: NSFileProviderItemVersion,
        options: NSFileProviderDeleteItemOptions,
        request: NSFileProviderRequest,
        completionHandler: @escaping (Error?) -> Void
    ) -> Progress {
        let progress = Progress(totalUnitCount: 1)
        // Finder delete maps to a Roughdash tombstone/delete-everywhere revision.
        completionHandler(NSFileProviderError(.serverUnreachable))
        progress.completedUnitCount = 1
        return progress
    }

    func enumerator(for containerItemIdentifier: NSFileProviderItemIdentifier, request: NSFileProviderRequest) throws -> NSFileProviderEnumerator {
        RoughdashEnumerator(containerIdentifier: containerItemIdentifier, domain: domain, catalog: catalog)
    }

    private func writeTemporaryContent(_ data: Data, for item: SyncItem) throws -> URL {
        let baseURL: URL
        if let manager = NSFileProviderManager(for: domain) {
            baseURL = try manager.temporaryDirectoryURL()
        } else {
            baseURL = FileManager.default.temporaryDirectory
        }

        let didAccess = baseURL.startAccessingSecurityScopedResource()
        defer {
            if didAccess {
                baseURL.stopAccessingSecurityScopedResource()
            }
        }

        let directory = baseURL.appendingPathComponent("RoughdashHydration", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let safeName = item.name.replacingOccurrences(of: "/", with: "-")
        let fileURL = directory.appendingPathComponent("\(item.id)-\(safeName)", isDirectory: false)
        try data.write(to: fileURL, options: .atomic)
        return fileURL
    }

    private static func verify(content data: Data, expectedHash: String) throws {
        guard isSHA256(expectedHash) else {
            return
        }
        let actualHash = SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
        guard actualHash == expectedHash else {
            throw HydrationError.hashMismatch
        }
    }

    private static func isSHA256(_ value: String) -> Bool {
        guard value.count == 64 else {
            return false
        }
        return value.unicodeScalars.allSatisfy { scalar in
            (48...57).contains(scalar.value) || (97...102).contains(scalar.value)
        }
    }
}

private final class FileProviderContentFetch: @unchecked Sendable {
    private let progress: Progress
    private let completionHandler: (URL?, NSFileProviderItem?, Error?) -> Void

    init(
        progress: Progress,
        completionHandler: @escaping (URL?, NSFileProviderItem?, Error?) -> Void
    ) {
        self.progress = progress
        self.completionHandler = completionHandler
    }

    func complete(_ url: URL?, _ item: NSFileProviderItem?, _ error: Error?) {
        progress.completedUnitCount = 1
        completionHandler(url, item, error)
    }
}

private enum HydrationError: Error, LocalizedError {
    case hashMismatch

    var errorDescription: String? {
        switch self {
        case .hashMismatch:
            return "Downloaded file hash did not match the sync catalog."
        }
    }
}
