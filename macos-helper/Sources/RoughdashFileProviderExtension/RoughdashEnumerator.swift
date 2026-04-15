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
        logger.info("Enumerating \(items.count, privacy: .public) changed items for \(self.containerIdentifier.rawValue, privacy: .public)")
        observer.didUpdate(items)
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
            return projectItems(from: snapshot) + snapshot.items
                .filter { !$0.tombstoned }
                .map { RoughdashProviderItem(item: $0) }
        }

        if let projectID = RoughdashFileProviderIdentifiers.projectID(from: containerIdentifier) {
            return snapshot.items
                .filter { $0.projectId == projectID && $0.parentId == "root" && !$0.tombstoned }
                .map { RoughdashProviderItem(item: $0) }
        }

        let parentID = containerIdentifier.rawValue
        return snapshot.items
            .filter { $0.parentId == parentID && !$0.tombstoned }
            .map { RoughdashProviderItem(item: $0) }
    }

    private func projectItems(from snapshot: HelperSnapshot) -> [NSFileProviderItem] {
        snapshot.projects
            .filter(\.enabled)
            .map { RoughdashProjectProviderItem(project: $0) }
    }

    private static func syncAnchor(for snapshot: HelperSnapshot) -> NSFileProviderSyncAnchor {
        let value = "roughdash:\(snapshot.updatedAt.timeIntervalSince1970):\(snapshot.projects.count):\(snapshot.items.count):\(snapshot.conflicts.count)"
        return NSFileProviderSyncAnchor(Data(value.utf8))
    }
}
