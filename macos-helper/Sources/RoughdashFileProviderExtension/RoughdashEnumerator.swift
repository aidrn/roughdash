import FileProvider
import Foundation
import OSLog
import RoughdashHelperCore

final class RoughdashEnumerator: NSObject, NSFileProviderEnumerator {
    private let logger = Logger(subsystem: "com.roughdash.helper", category: "file-provider")
    private let containerIdentifier: NSFileProviderItemIdentifier
    private let domain: NSFileProviderDomain
    private let catalog: LocalCatalog

    init(containerIdentifier: NSFileProviderItemIdentifier, domain: NSFileProviderDomain, catalog: LocalCatalog) {
        self.containerIdentifier = containerIdentifier
        self.domain = domain
        self.catalog = catalog
        super.init()
    }

    func invalidate() {}

    func enumerateItems(
        for observer: NSFileProviderEnumerationObserver,
        startingAt page: NSFileProviderPage
    ) {
        let snapshot = RoughdashExtensionStorage.readSnapshot(for: domain)
        let items = providerItems(from: snapshot)

        logger.info("Enumerating \(items.count, privacy: .public) items for \(self.containerIdentifier.rawValue, privacy: .public)")
        observer.didEnumerate(items)
        observer.finishEnumerating(upTo: nil)
    }

    func enumerateChanges(
        for observer: NSFileProviderChangeObserver,
        from syncAnchor: NSFileProviderSyncAnchor
    ) {
        let snapshot = RoughdashExtensionStorage.readSnapshot(for: domain)
        let items = providerItems(from: snapshot)
        let deletedIdentifiers = snapshot.items
            .filter(\.tombstoned)
            .map { NSFileProviderItemIdentifier($0.id) }
        logger.info("Enumerating \(items.count, privacy: .public) changed items and \(deletedIdentifiers.count, privacy: .public) deletes for \(self.containerIdentifier.rawValue, privacy: .public)")
        observer.didUpdate(items)
        if !deletedIdentifiers.isEmpty {
            observer.didDeleteItems(withIdentifiers: deletedIdentifiers)
        }
        observer.finishEnumeratingChanges(upTo: Self.syncAnchor(for: snapshot), moreComing: false)
    }

    func currentSyncAnchor(completionHandler: @escaping (NSFileProviderSyncAnchor?) -> Void) {
        let snapshot = RoughdashExtensionStorage.readSnapshot(for: domain)
        completionHandler(Self.syncAnchor(for: snapshot))
    }

    private func providerItems(from snapshot: HelperSnapshot) -> [NSFileProviderItem] {
        if containerIdentifier == .rootContainer {
            return projectItems(from: snapshot)
        }

        if containerIdentifier == .workingSet {
            return projectItems(from: snapshot) + providerItems(snapshot.items.filter { !$0.tombstoned }, snapshot: snapshot)
        }

        if let projectID = RoughdashFileProviderIdentifiers.projectID(from: containerIdentifier) {
            return providerItems(
                snapshot.items.filter { $0.projectId == projectID && $0.parentId == "root" && !$0.tombstoned },
                snapshot: snapshot
            )
        }

        let parentID = containerIdentifier.rawValue
        return providerItems(
            snapshot.items.filter { $0.parentId == parentID && !$0.tombstoned },
            snapshot: snapshot
        )
    }

    private func projectItems(from snapshot: HelperSnapshot) -> [NSFileProviderItem] {
        snapshot.projects
            .filter(\.enabled)
            .map { project in
                RoughdashProjectProviderItem(
                    project: project,
                    childCount: snapshot.items.filter { $0.projectId == project.id && $0.parentId == "root" && !$0.tombstoned }.count
                )
            }
    }

    private func providerItems(_ items: [SyncItem], snapshot: HelperSnapshot) -> [NSFileProviderItem] {
        items.map { item in
            RoughdashProviderItem(
                item: item,
                childCount: item.kind == "directory"
                    ? snapshot.items.filter { $0.parentId == item.id && !$0.tombstoned }.count
                    : nil
            )
        }
    }

    private static func syncAnchor(for snapshot: HelperSnapshot) -> NSFileProviderSyncAnchor {
        let value = "roughdash:\(snapshot.updatedAt.timeIntervalSince1970):\(snapshot.projects.count):\(snapshot.items.count):\(snapshot.conflicts.count)"
        return NSFileProviderSyncAnchor(Data(value.utf8))
    }
}
