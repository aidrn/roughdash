import FileProvider
import Foundation
import OSLog
import RoughdashHelperCore

final class RoughdashFileProviderExtension: NSObject, NSFileProviderReplicatedExtension {
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
        // The real implementation hydrates through TransferClient, writes to the
        // provider storage URL, and returns that file URL with the updated item.
        completionHandler(nil, nil, NSFileProviderError(.serverUnreachable))
        progress.completedUnitCount = 1
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
}
