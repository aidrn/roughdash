import CryptoKit
import FileProvider
import Foundation
import OSLog
import RoughdashHelperCore

final class RoughdashFileProviderExtension: NSObject, NSFileProviderReplicatedExtension, @unchecked Sendable {
    private let logger = Logger(subsystem: "com.roughdash.helper", category: "file-provider")
    private let domain: NSFileProviderDomain
    private let catalog = LocalCatalog()
    private let mutationLock = FileProviderMutationLock()

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
                try await mutationLock.withLock {
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

                    if let parentContext = Self.directorySyncContext(
                        for: parentIdentifier,
                        snapshot: snapshot
                    ), let parentDirectoryURL = try await resolvedDirectorySyncURL(
                        for: parentIdentifier,
                        provided: nil,
                        snapshot: snapshot
                    ) {
                        let didAccess = parentDirectoryURL.startAccessingSecurityScopedResource()
                        defer {
                            if didAccess {
                                parentDirectoryURL.stopAccessingSecurityScopedResource()
                            }
                        }

                        let result = try await syncDirectoryContainer(
                            at: parentDirectoryURL,
                            context: parentContext,
                            snapshot: &snapshot,
                            api: api
                        )
                        if let device = result.device {
                            snapshot.syncDevice = device
                        }
                        snapshot.updatedAt = Date()
                        try RoughdashExtensionStorage.writeSnapshot(snapshot, for: domain)
                        await signalWorkingSetChanged()

                        if let synced = Self.existingProviderItem(
                            for: parentIdentifier,
                            filename: filename,
                            snapshot: snapshot
                        ) {
                            logger.info("Created File Provider item \(filename, privacy: .public) via parent container sync")
                            creation.complete(synced, [], false, nil)
                            return
                        }
                    }

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

                    let contentURL = try await resolvedCreateContentURL(
                        provided: url,
                        parentIdentifier: parentIdentifier,
                        filename: filename,
                        snapshot: snapshot
                    )
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
                }
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
        let itemIdentifier = item.itemIdentifier
        let projectID = RoughdashFileProviderIdentifiers.projectID(from: item.itemIdentifier)
        let baseContentHash = String(data: version.contentVersion, encoding: .utf8)

        Task {
            do {
                try await mutationLock.withLock {
                    var snapshot = RoughdashExtensionStorage.readSnapshot(for: domain)
                    guard let serverURL = URL(string: snapshot.serverURL),
                          !snapshot.helperID.isEmpty,
                          let helperToken = snapshot.helperToken,
                          !helperToken.isEmpty else {
                        throw NSFileProviderError(.notAuthenticated)
                    }
                    let api = RoughdashAPIClient(baseURL: serverURL, helperID: snapshot.helperID, helperToken: helperToken)

                    if let projectID,
                       let project = snapshot.projects.first(where: { $0.id == projectID && $0.enabled }) {
                        guard let contentURL = try await resolvedDirectorySyncURL(
                            for: itemIdentifier,
                            provided: newContents,
                            snapshot: snapshot
                        ) else {
                            logger.info("Accepted metadata-only File Provider modify for project \(projectID, privacy: .public)")
                            modification.complete(Self.projectProviderItem(projectID: projectID, snapshot: snapshot), [], false, nil)
                            return
                        }

                        let didAccess = contentURL.startAccessingSecurityScopedResource()
                        defer {
                            if didAccess {
                                contentURL.stopAccessingSecurityScopedResource()
                            }
                        }

                        let result = try await syncDirectoryContainer(
                            at: contentURL,
                            context: DirectorySyncContext(
                                projectID: project.id,
                                parentID: "root",
                                relativePathPrefix: ""
                            ),
                            snapshot: &snapshot,
                            api: api
                        )
                        if let device = result.device {
                            snapshot.syncDevice = device
                        }
                        snapshot.updatedAt = Date()
                        try RoughdashExtensionStorage.writeSnapshot(snapshot, for: domain)
                        await signalWorkingSetChanged()

                        logger.info("Synced File Provider project container \(projectID, privacy: .public)")
                        modification.complete(Self.projectProviderItem(projectID: projectID, snapshot: snapshot), [], false, nil)
                        return
                    }

                    guard let existing = snapshot.items.first(where: { $0.id == itemID && !$0.tombstoned }) else {
                        throw NSFileProviderError(.noSuchItem)
                    }
                    guard let contentURL = newContents else {
                        if existing.kind == "directory",
                           let directoryURL = try await resolvedDirectorySyncURL(
                               for: itemIdentifier,
                               provided: nil,
                               snapshot: snapshot
                           ) {
                            let didAccess = directoryURL.startAccessingSecurityScopedResource()
                            defer {
                                if didAccess {
                                    directoryURL.stopAccessingSecurityScopedResource()
                                }
                            }

                            let result = try await syncDirectoryContainer(
                                at: directoryURL,
                                context: DirectorySyncContext(
                                    projectID: existing.projectId,
                                    parentID: existing.id,
                                    relativePathPrefix: existing.relativePath
                                ),
                                snapshot: &snapshot,
                                api: api
                            )
                            if let device = result.device {
                                snapshot.syncDevice = device
                            }
                            snapshot.updatedAt = Date()
                            try RoughdashExtensionStorage.writeSnapshot(snapshot, for: domain)
                            await signalWorkingSetChanged()

                            logger.info("Synced File Provider directory \(itemID, privacy: .public) via metadata-only modify")
                            modification.complete(Self.providerItem(for: existing, snapshot: snapshot), [], false, nil)
                            return
                        }

                        logger.info("Accepted metadata-only File Provider modify for \(itemID, privacy: .public)")
                        modification.complete(Self.providerItem(for: existing, snapshot: snapshot), [], false, nil)
                        return
                    }

                    if existing.kind == "directory" {
                        guard let directoryURL = try await resolvedDirectorySyncURL(
                            for: itemIdentifier,
                            provided: contentURL,
                            snapshot: snapshot
                        ) else {
                            throw NSFileProviderError(.cannotSynchronize)
                        }

                        let didAccess = directoryURL.startAccessingSecurityScopedResource()
                        defer {
                            if didAccess {
                                directoryURL.stopAccessingSecurityScopedResource()
                            }
                        }

                        let result = try await syncDirectoryContainer(
                            at: directoryURL,
                            context: DirectorySyncContext(
                                projectID: existing.projectId,
                                parentID: existing.id,
                                relativePathPrefix: existing.relativePath
                            ),
                            snapshot: &snapshot,
                            api: api
                        )
                        if let device = result.device {
                            snapshot.syncDevice = device
                        }
                        snapshot.updatedAt = Date()
                        try RoughdashExtensionStorage.writeSnapshot(snapshot, for: domain)
                        await signalWorkingSetChanged()

                        logger.info("Synced File Provider directory \(itemID, privacy: .public)")
                        modification.complete(Self.providerItem(for: existing, snapshot: snapshot), [], false, nil)
                        return
                    }

                    let didAccess = contentURL.startAccessingSecurityScopedResource()
                    defer {
                        if didAccess {
                            contentURL.stopAccessingSecurityScopedResource()
                        }
                    }

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
                    modification.complete(Self.providerItem(for: upload.completed.item, snapshot: snapshot), [], false, nil)
                }
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

    private static func isDirectoryURL(_ url: URL) -> Bool {
        var isDirectory = ObjCBool(false)
        return FileManager.default.fileExists(atPath: url.path, isDirectory: &isDirectory) && isDirectory.boolValue
    }

    private static func isExistingRegularFileURL(_ url: URL) -> Bool {
        var isDirectory = ObjCBool(false)
        return FileManager.default.fileExists(atPath: url.path, isDirectory: &isDirectory) && !isDirectory.boolValue
    }

    private static func upsert(_ item: SyncItem, in items: inout [SyncItem]) {
        if let index = items.firstIndex(where: { $0.id == item.id || $0.relativePath == item.relativePath }) {
            items[index] = item
        } else {
            items.append(item)
        }
    }

    private func syncDirectoryContainer(
        at directoryURL: URL,
        context: DirectorySyncContext,
        snapshot: inout HelperSnapshot,
        api: RoughdashAPIClient
    ) async throws -> DirectorySyncResult {
        logger.info("Scanning local File Provider directory \(Self.localPathState(directoryURL), privacy: .public) for prefix \(context.relativePathPrefix, privacy: .public)")
        try await refreshProjectItems(projectID: context.projectID, snapshot: &snapshot, api: api)

        var syncedDevice: SyncDevice?
        let children: [URL]
        do {
            children = try FileManager.default.contentsOfDirectory(
                at: directoryURL,
                includingPropertiesForKeys: [.isDirectoryKey, .isRegularFileKey, .isSymbolicLinkKey],
                options: []
            ).sorted { $0.lastPathComponent.localizedCaseInsensitiveCompare($1.lastPathComponent) == .orderedAscending }
        } catch {
            logger.error("Could not enumerate local File Provider directory \(Self.localPathState(directoryURL), privacy: .public): \(Self.describe(error), privacy: .public)")
            throw error
        }

        for childURL in children {
            let effectiveChildURL = Self.rewriteToUserFacingURL(childURL, snapshot: snapshot) ?? childURL
            let didAccessChild = effectiveChildURL.startAccessingSecurityScopedResource()
            defer {
                if didAccessChild {
                    effectiveChildURL.stopAccessingSecurityScopedResource()
                }
            }

            let name = effectiveChildURL.lastPathComponent
            guard !Self.shouldIgnoreLocalEntry(named: name) else {
                continue
            }

            let values = try effectiveChildURL.resourceValues(forKeys: [.isDirectoryKey, .isRegularFileKey, .isSymbolicLinkKey])
            guard values.isSymbolicLink != true else {
                logger.info("Skipping symbolic link during File Provider sync: \(effectiveChildURL.path, privacy: .public)")
                continue
            }

            let relativePath = context.relativePath(for: name)
            do {
                if values.isDirectory == true {
                    let directoryItem = try await ensureDirectory(
                        relativePath: relativePath,
                        context: context,
                        snapshot: &snapshot,
                        api: api,
                        syncedDevice: &syncedDevice
                    )
                    let childResult = try await syncDirectoryContainer(
                        at: effectiveChildURL,
                        context: DirectorySyncContext(
                            projectID: context.projectID,
                            parentID: directoryItem.id,
                            relativePathPrefix: directoryItem.relativePath
                        ),
                        snapshot: &snapshot,
                        api: api
                    )
                    syncedDevice = childResult.device ?? syncedDevice
                } else if values.isRegularFile == true {
                    let device = try await syncFile(
                        at: effectiveChildURL,
                        relativePath: relativePath,
                        context: context,
                        snapshot: &snapshot,
                        api: api
                    )
                    syncedDevice = device ?? syncedDevice
                }
            } catch {
                logger.error("Could not sync local File Provider child \(effectiveChildURL.path, privacy: .public): \(error.localizedDescription, privacy: .public)")
            }
        }

        return DirectorySyncResult(device: syncedDevice)
    }

    private func refreshProjectItems(
        projectID: String,
        snapshot: inout HelperSnapshot,
        api: RoughdashAPIClient
    ) async throws {
        let refreshedItems = try await api.listItems(projectID: projectID, recursive: true)
        snapshot.items.removeAll { $0.projectId == projectID }
        snapshot.items.append(contentsOf: refreshedItems)
    }

    private func ensureDirectory(
        relativePath: String,
        context: DirectorySyncContext,
        snapshot: inout HelperSnapshot,
        api: RoughdashAPIClient,
        syncedDevice: inout SyncDevice?
    ) async throws -> SyncItem {
        if let existing = Self.remoteItem(projectID: context.projectID, relativePath: relativePath, snapshot: snapshot) {
            guard existing.kind == "directory" else {
                throw NSFileProviderError(.cannotSynchronize)
            }
            return existing
        }

        do {
            let created = try await SyncUploadCoordinator(api: api).createDirectory(
                snapshot: snapshot,
                projectID: context.projectID,
                parentID: context.parentID,
                relativePath: relativePath
            )
            syncedDevice = created.device
            Self.upsert(created.directory.item, in: &snapshot.items)
            logger.info("Created File Provider directory \(created.directory.item.relativePath, privacy: .public) from local container sync")
            return created.directory.item
        } catch SyncUploadError.itemAlreadyExists {
            try await refreshProjectItems(projectID: context.projectID, snapshot: &snapshot, api: api)
            if let existing = Self.remoteItem(projectID: context.projectID, relativePath: relativePath, snapshot: snapshot), existing.kind == "directory" {
                return existing
            }
            throw NSFileProviderError(.cannotSynchronize)
        }
    }

    private func syncFile(
        at fileURL: URL,
        relativePath: String,
        context: DirectorySyncContext,
        snapshot: inout HelperSnapshot,
        api: RoughdashAPIClient
    ) async throws -> SyncDevice? {
        let effectiveFileURL = Self.rewriteToUserFacingURL(fileURL, snapshot: snapshot) ?? fileURL
        let didAccess = effectiveFileURL.startAccessingSecurityScopedResource()
        defer {
            if didAccess {
                effectiveFileURL.stopAccessingSecurityScopedResource()
            }
        }

        logger.info("Syncing local File Provider file \(relativePath, privacy: .public): \(Self.localPathState(effectiveFileURL), privacy: .public)")

        do {
            if let existing = Self.remoteItem(projectID: context.projectID, relativePath: relativePath, snapshot: snapshot) {
                guard existing.kind == "file" else {
                    throw NSFileProviderError(.cannotSynchronize)
                }
                guard try Self.shouldUploadFile(at: effectiveFileURL, comparedTo: existing) else {
                    return nil
                }
                let upload = try await SyncUploadCoordinator(api: api).uploadExistingFile(
                    snapshot: snapshot,
                    item: existing,
                    fileURL: effectiveFileURL,
                    baseContentHash: existing.contentHash
                )
                Self.upsert(upload.completed.item, in: &snapshot.items)
                logger.info("Uploaded modified File Provider file \(upload.completed.item.relativePath, privacy: .public) from local container sync")
                return upload.device
            }

            do {
                let upload = try await SyncUploadCoordinator(api: api).uploadNewFile(
                    snapshot: snapshot,
                    projectID: context.projectID,
                    parentID: context.parentID,
                    relativePath: relativePath,
                    fileURL: effectiveFileURL,
                )
                Self.upsert(upload.completed.item, in: &snapshot.items)
                logger.info("Created File Provider file \(upload.completed.item.relativePath, privacy: .public) from local container sync")
                return upload.device
            } catch SyncUploadError.itemAlreadyExists {
                try await refreshProjectItems(projectID: context.projectID, snapshot: &snapshot, api: api)
                if let existing = Self.remoteItem(projectID: context.projectID, relativePath: relativePath, snapshot: snapshot), existing.kind == "file" {
                    let upload = try await SyncUploadCoordinator(api: api).uploadExistingFile(
                        snapshot: snapshot,
                        item: existing,
                        fileURL: effectiveFileURL,
                        baseContentHash: existing.contentHash
                    )
                    Self.upsert(upload.completed.item, in: &snapshot.items)
                    return upload.device
                }
                throw NSFileProviderError(.cannotSynchronize)
            }
        } catch {
            logger.error("Local File Provider file sync failed for \(relativePath, privacy: .public): \(Self.describe(error), privacy: .public) | \(Self.localPathState(effectiveFileURL), privacy: .public)")
            throw error
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

    private static func shouldUploadFile(at fileURL: URL, comparedTo item: SyncItem) throws -> Bool {
        let attributes = try FileManager.default.attributesOfItem(atPath: fileURL.path)
        let localSize = (attributes[.size] as? NSNumber)?.int64Value ?? 0
        if localSize != item.size {
            return true
        }

        guard isSHA256(item.contentHash) else {
            return true
        }

        let localHash = try CoordinatedFileReader.sha256Hex(at: fileURL)
        return localHash != item.contentHash
    }

    private static func shouldIgnoreLocalEntry(named name: String) -> Bool {
        if name == ".roughdash" || name == ".DS_Store" || name == ".Spotlight-V100" || name == ".Trashes" || name == ".fseventsd" || name == ".TemporaryItems" || name == ".Trash" {
            return true
        }
        if name.hasSuffix(".part") || name.hasSuffix(".swp") || name.hasSuffix(".swo") || name.hasSuffix("~") || name.hasPrefix(".#") {
            return true
        }
        return false
    }

    private static func remoteItem(projectID: String, relativePath: String, snapshot: HelperSnapshot) -> SyncItem? {
        snapshot.items.first(where: { $0.projectId == projectID && $0.relativePath == relativePath && !$0.tombstoned })
    }

    private static func localPathState(_ url: URL) -> String {
        let fileManager = FileManager.default
        var isDirectory = ObjCBool(false)
        let exists = fileManager.fileExists(atPath: url.path, isDirectory: &isDirectory)
        let readable = fileManager.isReadableFile(atPath: url.path)
        let writable = fileManager.isWritableFile(atPath: url.path)
        let resolvedPath = url.resolvingSymlinksInPath().path
        let attributes = try? fileManager.attributesOfItem(atPath: url.path)
        let size = (attributes?[.size] as? NSNumber)?.stringValue ?? "nil"
        let type = (attributes?[.type] as? FileAttributeType)?.rawValue ?? "nil"
        let modificationDate = (attributes?[.modificationDate] as? Date)?.description ?? "nil"
        return "path=\(url.path) resolved=\(resolvedPath) exists=\(exists) isDirectory=\(isDirectory.boolValue) readable=\(readable) writable=\(writable) size=\(size) type=\(type) mtime=\(modificationDate)"
    }

    private static func describe(_ error: Error) -> String {
        let nsError = error as NSError
        return "\(error.localizedDescription) (domain=\(nsError.domain), code=\(nsError.code))"
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

    private static func directorySyncContext(
        for identifier: NSFileProviderItemIdentifier,
        snapshot: HelperSnapshot
    ) -> DirectorySyncContext? {
        if let projectID = RoughdashFileProviderIdentifiers.projectID(from: identifier),
           snapshot.projects.contains(where: { $0.id == projectID && $0.enabled }) {
            return DirectorySyncContext(
                projectID: projectID,
                parentID: "root",
                relativePathPrefix: ""
            )
        }

        guard let parent = snapshot.items.first(where: { $0.id == identifier.rawValue && $0.kind == "directory" && !$0.tombstoned }) else {
            return nil
        }

        return DirectorySyncContext(
            projectID: parent.projectId,
            parentID: parent.id,
            relativePathPrefix: parent.relativePath
        )
    }

    private static func projectProviderItem(projectID: String, snapshot: HelperSnapshot) -> NSFileProviderItem? {
        guard let project = snapshot.projects.first(where: { $0.id == projectID && $0.enabled }) else {
            return nil
        }
        return RoughdashProjectProviderItem(
            project: project,
            childCount: snapshot.items.filter { $0.projectId == projectID && $0.parentId == "root" && !$0.tombstoned }.count
        )
    }

    private static func providerItem(for item: SyncItem, snapshot: HelperSnapshot) -> NSFileProviderItem {
        RoughdashProviderItem(
            item: item,
            childCount: item.kind == "directory"
                ? snapshot.items.filter { $0.parentId == item.id && !$0.tombstoned }.count
                : nil
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
            return Self.projectProviderItem(projectID: project.id, snapshot: snapshot)
        }

        if let projectID = RoughdashFileProviderIdentifiers.projectID(from: parentIdentifier),
           let existing = snapshot.items.first(where: {
               $0.projectId == projectID && $0.parentId == "root" && $0.name == cleanName && !$0.tombstoned
           }) {
            return Self.providerItem(for: existing, snapshot: snapshot)
        }

        if let parent = snapshot.items.first(where: { $0.id == parentIdentifier.rawValue && !$0.tombstoned }),
           let existing = snapshot.items.first(where: {
               $0.parentId == parent.id && $0.name == cleanName && !$0.tombstoned
           }) {
            return Self.providerItem(for: existing, snapshot: snapshot)
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

    private func resolvedCreateContentURL(
        provided url: URL?,
        parentIdentifier: NSFileProviderItemIdentifier,
        filename: String,
        snapshot: HelperSnapshot
    ) async throws -> URL {
        if let url, Self.isExistingRegularFileURL(url) {
            return url
        }

        if let fallbackURL = try await materializedChildURL(
            parentIdentifier: parentIdentifier,
            filename: filename,
            snapshot: snapshot
        ), Self.isExistingRegularFileURL(fallbackURL) {
            logger.info("Using materialized File Provider path for \(filename, privacy: .public): \(fallbackURL.path, privacy: .public)")
            return fallbackURL
        }

        throw NSFileProviderError(.cannotSynchronize)
    }

    private func resolvedDirectorySyncURL(
        for identifier: NSFileProviderItemIdentifier,
        provided url: URL?,
        snapshot: HelperSnapshot
    ) async throws -> URL? {
        if let url, Self.isDirectoryURL(url) {
            return url
        }

        if let preferredURL = Self.userFacingURL(
            for: identifier,
            snapshot: snapshot
        ), Self.isDirectoryURL(preferredURL) {
            logger.info("Using user-facing File Provider directory for \(identifier.rawValue, privacy: .public): \(preferredURL.path, privacy: .public)")
            return preferredURL
        }

        if let fallbackURL = try await userVisibleURL(for: identifier),
           let rewrittenURL = Self.rewriteToUserFacingURL(fallbackURL, snapshot: snapshot),
           Self.isDirectoryURL(rewrittenURL) {
            logger.info("Using rewritten user-facing File Provider directory for \(identifier.rawValue, privacy: .public): \(rewrittenURL.path, privacy: .public)")
            return rewrittenURL
        }

        if let fallbackURL = try await userVisibleURL(for: identifier),
           Self.isDirectoryURL(fallbackURL) {
            logger.info("Using materialized File Provider directory for \(identifier.rawValue, privacy: .public): \(fallbackURL.path, privacy: .public)")
            return fallbackURL
        }

        return nil
    }

    private func materializedChildURL(
        parentIdentifier: NSFileProviderItemIdentifier,
        filename: String,
        snapshot: HelperSnapshot
    ) async throws -> URL? {
        if let parentURL = Self.userFacingURL(for: parentIdentifier, snapshot: snapshot) {
            return parentURL.appendingPathComponent(filename, isDirectory: false)
        }

        guard let parentURL = try await userVisibleURL(for: parentIdentifier) else {
            return nil
        }

        let effectiveParentURL = Self.rewriteToUserFacingURL(parentURL, snapshot: snapshot) ?? parentURL
        return effectiveParentURL.appendingPathComponent(filename, isDirectory: false)
    }

    private func userVisibleURL(
        for identifier: NSFileProviderItemIdentifier
    ) async throws -> URL? {
        guard let manager = NSFileProviderManager(for: domain) else {
            return nil
        }

        return try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<URL?, Error>) in
            manager.getUserVisibleURL(for: identifier) { url, error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume(returning: url)
                }
            }
        }
    }

    private static func userFacingURL(
        for identifier: NSFileProviderItemIdentifier,
        snapshot: HelperSnapshot
    ) -> URL? {
        guard let selectedVolumePath = snapshot.selectedVolumePath else {
            return nil
        }

        let volumeURL = URL(fileURLWithPath: selectedVolumePath, isDirectory: true)
        guard let domainRootURL = userFacingDomainRootURL(on: volumeURL) else {
            return nil
        }

        if identifier == .rootContainer {
            return domainRootURL
        }

        if let projectID = RoughdashFileProviderIdentifiers.projectID(from: identifier),
           let project = snapshot.projects.first(where: { $0.id == projectID && $0.enabled }) {
            return domainRootURL.appendingPathComponent(project.name, isDirectory: true)
        }

        guard let item = snapshot.items.first(where: { $0.id == identifier.rawValue && !$0.tombstoned }) else {
            return nil
        }

        return domainRootURL.appendingPathComponent(item.relativePath, isDirectory: item.kind == "directory")
    }

    private static func userFacingDomainRootURL(on volumeURL: URL) -> URL? {
        guard let children = try? FileManager.default.contentsOfDirectory(
            at: volumeURL,
            includingPropertiesForKeys: [.isSymbolicLinkKey],
            options: []
        ) else {
            return nil
        }

        for childURL in children {
            guard (try? childURL.resourceValues(forKeys: [.isSymbolicLinkKey]).isSymbolicLink) == true else {
                continue
            }

            guard let destination = try? FileManager.default.destinationOfSymbolicLink(atPath: childURL.path) else {
                continue
            }

            if destination.hasPrefix(".CloudStorage/Data/") || destination.contains("/.CloudStorage/Data/") {
                return childURL
            }
        }

        return nil
    }

    private static func rewriteToUserFacingURL(
        _ url: URL,
        snapshot: HelperSnapshot
    ) -> URL? {
        guard let selectedVolumePath = snapshot.selectedVolumePath else {
            return nil
        }

        let backingPrefix = selectedVolumePath + "/.CloudStorage/Data/"
        guard url.path.hasPrefix(backingPrefix) else {
            return nil
        }

        let suffix = String(url.path.dropFirst(backingPrefix.count))
        let components = suffix.split(separator: "/", maxSplits: 1, omittingEmptySubsequences: false)
        guard let domainFolder = components.first, !domainFolder.isEmpty else {
            return nil
        }

        var rewrittenPath = selectedVolumePath + "/" + domainFolder
        if components.count > 1 {
            rewrittenPath += "/" + components[1]
        }
        return URL(fileURLWithPath: rewrittenPath, isDirectory: false)
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

private struct DirectorySyncContext: Sendable {
    var projectID: String
    var parentID: String
    var relativePathPrefix: String

    func relativePath(for childName: String) -> String {
        relativePathPrefix.isEmpty ? childName : "\(relativePathPrefix)/\(childName)"
    }
}

private struct DirectorySyncResult: Sendable {
    var device: SyncDevice?
}

private actor FileProviderMutationLock {
    private var isLocked = false
    private var waiters: [CheckedContinuation<Void, Never>] = []

    func withLock<T>(
        _ operation: @Sendable () async throws -> T
    ) async throws -> T {
        await acquire()
        defer { release() }
        return try await operation()
    }

    private func acquire() async {
        if !isLocked {
            isLocked = true
            return
        }

        await withCheckedContinuation { continuation in
            waiters.append(continuation)
        }
    }

    private func release() {
        if let waiter = waiters.first {
            waiters.removeFirst()
            waiter.resume()
        } else {
            isLocked = false
        }
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
