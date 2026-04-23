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
            completionHandler(nil, NSError(domain: NSCocoaErrorDomain, code: NSFeatureUnsupportedError))
        } else if let projectID = RoughdashFileProviderIdentifiers.projectID(from: identifier),
                  let project = snapshot.projects.first(where: { $0.id == projectID && $0.enabled }) {
            completionHandler(
                RoughdashProjectProviderItem(
                    project: project,
                    childCount: snapshot.items.filter { $0.projectId == projectID && $0.parentId == "root" && !$0.tombstoned }.count
                ),
                nil
            )
        } else if let item = snapshot.items.first(where: { $0.id == identifier.rawValue && !$0.tombstoned }) {
            completionHandler(
                RoughdashProviderItem(
                    item: item,
                    childCount: item.kind == "directory"
                        ? snapshot.items.filter { $0.parentId == item.id && !$0.tombstoned }.count
                        : nil
                ),
                nil
            )
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
                await signalWorkingSetChanged()
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
        let creation = FileProviderItemModification(progress: progress, completionHandler: completionHandler)
        let filename = itemTemplate.filename
        let parentIdentifier = itemTemplate.parentItemIdentifier
        let createsDirectory = Self.isDirectoryCreate(itemTemplate: itemTemplate, contents: url)

        Task {
            do {
                var snapshot = RoughdashExtensionStorage.readSnapshot(for: domain)
                guard let serverURL = URL(string: snapshot.serverURL),
                      !snapshot.helperID.isEmpty,
                      let helperToken = snapshot.helperToken,
                      !helperToken.isEmpty else {
                    throw NSFileProviderError(.notAuthenticated)
                }

                if let existing = Self.existingProviderItem(
                    for: parentIdentifier,
                    filename: filename,
                    snapshot: snapshot
                ) {
                    logger.info("Accepted File Provider import for existing item \(filename, privacy: .public)")
                    creation.complete(existing, [], false, nil)
                    return
                }

                let parent = try Self.uploadParentContext(
                    for: parentIdentifier,
                    filename: filename,
                    snapshot: snapshot
                )
                let api = RoughdashAPIClient(baseURL: serverURL, helperID: snapshot.helperID, helperToken: helperToken)

                if createsDirectory {
                    let created = try await SyncUploadCoordinator(api: api).createDirectory(
                        snapshot: snapshot,
                        projectID: parent.projectID,
                        parentID: parent.parentID,
                        relativePath: parent.relativePath
                    )
                    snapshot.syncDevice = created.device
                    Self.upsert(created.directory.item, in: &snapshot.items)
                    snapshot.updatedAt = Date()
                    try RoughdashExtensionStorage.writeSnapshot(snapshot, for: domain)
                    await signalWorkingSetChanged()

                    logger.info("Created File Provider directory \(created.directory.item.id, privacy: .public) at \(created.directory.item.relativePath, privacy: .public)")
                    creation.complete(
                        RoughdashProviderItem(item: created.directory.item, childCount: 0),
                        [],
                        false,
                        nil
                    )
                    return
                }

                guard let contentURL = url else {
                    throw NSFileProviderError(.cannotSynchronize)
                }
                var isDirectory = ObjCBool(false)
                guard FileManager.default.fileExists(atPath: contentURL.path, isDirectory: &isDirectory),
                      !isDirectory.boolValue else {
                    throw NSFileProviderError(.cannotSynchronize)
                }
                let didAccess = contentURL.startAccessingSecurityScopedResource()
                defer {
                    if didAccess {
                        contentURL.stopAccessingSecurityScopedResource()
                    }
                }

                let upload = try await SyncUploadCoordinator(api: api).uploadNewFile(
                    snapshot: snapshot,
                    projectID: parent.projectID,
                    parentID: parent.parentID,
                    relativePath: parent.relativePath,
                    fileURL: contentURL
                )
                snapshot.syncDevice = upload.device
                Self.upsert(upload.completed.item, in: &snapshot.items)
                snapshot.updatedAt = Date()
                try RoughdashExtensionStorage.writeSnapshot(snapshot, for: domain)
                await signalWorkingSetChanged()

                logger.info("Created File Provider item \(upload.completed.item.id, privacy: .public) at \(upload.completed.item.relativePath, privacy: .public)")
                creation.complete(RoughdashProviderItem(item: upload.completed.item), [], false, nil)
            } catch {
                logger.error("Could not create File Provider item \(filename, privacy: .public): \(error.localizedDescription, privacy: .public)")
                creation.complete(nil, fields, false, Self.fileProviderError(for: error))
            }
        }
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
        let modification = FileProviderItemModification(progress: progress, completionHandler: completionHandler)
        let itemID = item.itemIdentifier.rawValue
        let baseContentHash = String(data: version.contentVersion, encoding: .utf8)

        Task {
            do {
                var snapshot = RoughdashExtensionStorage.readSnapshot(for: domain)
                guard let existing = snapshot.items.first(where: { $0.id == itemID && !$0.tombstoned }) else {
                    throw NSFileProviderError(.noSuchItem)
                }
                guard let contentURL = newContents else {
                    logger.info("Accepted metadata-only File Provider modify for \(itemID, privacy: .public)")
                    modification.complete(RoughdashProviderItem(item: existing), [], false, nil)
                    return
                }
                guard let serverURL = URL(string: snapshot.serverURL),
                      !snapshot.helperID.isEmpty,
                      let helperToken = snapshot.helperToken,
                      !helperToken.isEmpty else {
                    throw NSFileProviderError(.notAuthenticated)
                }

                let didAccess = contentURL.startAccessingSecurityScopedResource()
                defer {
                    if didAccess {
                        contentURL.stopAccessingSecurityScopedResource()
                    }
                }

                let api = RoughdashAPIClient(baseURL: serverURL, helperID: snapshot.helperID, helperToken: helperToken)
                let upload = try await SyncUploadCoordinator(api: api).uploadExistingFile(
                    snapshot: snapshot,
                    item: existing,
                    fileURL: contentURL,
                    baseContentHash: baseContentHash
                )
                snapshot.syncDevice = upload.device
                Self.upsert(upload.completed.item, in: &snapshot.items)
                snapshot.updatedAt = Date()
                try RoughdashExtensionStorage.writeSnapshot(snapshot, for: domain)
                await signalWorkingSetChanged()

                logger.info("Uploaded File Provider item \(itemID, privacy: .public) as revision \(upload.completed.item.revision, privacy: .public)")
                modification.complete(RoughdashProviderItem(item: upload.completed.item), [], false, nil)
            } catch {
                logger.error("Could not upload File Provider item \(itemID, privacy: .public): \(error.localizedDescription, privacy: .public)")
                modification.complete(nil, changedFields, false, Self.fileProviderError(for: error))
            }
        }

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
        if containerItemIdentifier == .trashContainer {
            throw NSError(domain: NSCocoaErrorDomain, code: NSFeatureUnsupportedError)
        }
        return RoughdashEnumerator(containerIdentifier: containerItemIdentifier, domain: domain, catalog: catalog)
    }

    private static func isDirectoryCreate(itemTemplate: NSFileProviderItem, contents url: URL?) -> Bool {
        if itemTemplate.contentType == .folder {
            return true
        }
        guard let url else {
            return false
        }
        var isDirectory = ObjCBool(false)
        return FileManager.default.fileExists(atPath: url.path, isDirectory: &isDirectory) && isDirectory.boolValue
    }

    private static func upsert(_ item: SyncItem, in items: inout [SyncItem]) {
        if let index = items.firstIndex(where: { $0.id == item.id || $0.relativePath == item.relativePath }) {
            items[index] = item
        } else {
            items.append(item)
        }
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

    private static func uploadParentContext(
        for parentIdentifier: NSFileProviderItemIdentifier,
        filename: String,
        snapshot: HelperSnapshot
    ) throws -> UploadParentContext {
        let cleanName = filename.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !cleanName.isEmpty, !cleanName.contains("/") else {
            throw NSFileProviderError(.cannotSynchronize)
        }

        if let projectID = RoughdashFileProviderIdentifiers.projectID(from: parentIdentifier),
           snapshot.projects.contains(where: { $0.id == projectID && $0.enabled }) {
            return UploadParentContext(
                projectID: projectID,
                parentID: "root",
                relativePath: cleanName
            )
        }

        guard let parent = snapshot.items.first(where: { $0.id == parentIdentifier.rawValue && $0.kind == "directory" && !$0.tombstoned }) else {
            throw NSFileProviderError(.noSuchItem)
        }
        return UploadParentContext(
            projectID: parent.projectId,
            parentID: parent.id,
            relativePath: "\(parent.relativePath)/\(cleanName)"
        )
    }

    private static func existingProviderItem(
        for parentIdentifier: NSFileProviderItemIdentifier,
        filename: String,
        snapshot: HelperSnapshot
    ) -> NSFileProviderItem? {
        let cleanName = filename.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !cleanName.isEmpty else {
            return nil
        }

        if parentIdentifier == .rootContainer,
           let project = snapshot.projects.first(where: { $0.enabled && $0.name == cleanName }) {
            return RoughdashProjectProviderItem(
                project: project,
                childCount: snapshot.items.filter { $0.projectId == project.id && $0.parentId == "root" && !$0.tombstoned }.count
            )
        }

        if let projectID = RoughdashFileProviderIdentifiers.projectID(from: parentIdentifier),
           let existing = snapshot.items.first(where: {
               $0.projectId == projectID && $0.parentId == "root" && $0.name == cleanName && !$0.tombstoned
           }) {
            return RoughdashProviderItem(
                item: existing,
                childCount: existing.kind == "directory"
                    ? snapshot.items.filter { $0.parentId == existing.id && !$0.tombstoned }.count
                    : nil
            )
        }

        if let parent = snapshot.items.first(where: { $0.id == parentIdentifier.rawValue && !$0.tombstoned }),
           let existing = snapshot.items.first(where: {
               $0.parentId == parent.id && $0.name == cleanName && !$0.tombstoned
           }) {
            return RoughdashProviderItem(
                item: existing,
                childCount: existing.kind == "directory"
                    ? snapshot.items.filter { $0.parentId == existing.id && !$0.tombstoned }.count
                    : nil
            )
        }

        return nil
    }

    private func signalWorkingSetChanged() async {
        guard let manager = NSFileProviderManager(for: domain) else {
            return
        }

        do {
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
                manager.signalEnumerator(for: .workingSet) { error in
                    if let error {
                        continuation.resume(throwing: error)
                    } else {
                        continuation.resume()
                    }
                }
            }
        } catch {
            logger.error("Could not signal File Provider working set after upload: \(error.localizedDescription, privacy: .public)")
        }
    }

    private static func fileProviderError(for error: Error) -> Error {
        if let fileProviderError = error as? NSFileProviderError {
            return fileProviderError
        }
        if let apiError = error as? APIError {
            switch apiError {
            case .server(let status, _) where status == 401 || status == 403:
                return NSFileProviderError(.notAuthenticated)
            case .server(let status, _) where status == 409:
                return NSFileProviderError(.cannotSynchronize)
            case .server:
                return NSFileProviderError(.serverUnreachable)
            case .invalidURL, .invalidResponse:
                return NSFileProviderError(.serverUnreachable)
            }
        }
        return NSFileProviderError(.cannotSynchronize)
    }
}

@available(macOS 15.0, *)
extension RoughdashFileProviderExtension: NSFileProviderExternalVolumeHandling {
    func shouldConnectExternalDomain(completionHandler: @escaping (Error?) -> Void) {
        completionHandler(nil)
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

private final class FileProviderItemModification: @unchecked Sendable {
    private let progress: Progress
    private let completionHandler: (NSFileProviderItem?, NSFileProviderItemFields, Bool, Error?) -> Void

    init(
        progress: Progress,
        completionHandler: @escaping (NSFileProviderItem?, NSFileProviderItemFields, Bool, Error?) -> Void
    ) {
        self.progress = progress
        self.completionHandler = completionHandler
    }

    func complete(_ item: NSFileProviderItem?, _ remainingFields: NSFileProviderItemFields, _ shouldFetchContent: Bool, _ error: Error?) {
        progress.completedUnitCount = 1
        completionHandler(item, remainingFields, shouldFetchContent, error)
    }
}

private struct UploadParentContext: Sendable {
    var projectID: String
    var parentID: String
    var relativePath: String
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
