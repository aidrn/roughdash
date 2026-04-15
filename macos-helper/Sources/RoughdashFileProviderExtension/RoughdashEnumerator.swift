import FileProvider
import Foundation
import RoughdashHelperCore

final class RoughdashEnumerator: NSObject, NSFileProviderEnumerator {
    private let containerIdentifier: NSFileProviderItemIdentifier
    private let catalog: LocalCatalog

    init(containerIdentifier: NSFileProviderItemIdentifier, catalog: LocalCatalog) {
        self.containerIdentifier = containerIdentifier
        self.catalog = catalog
        super.init()
    }

    func invalidate() {}

    func enumerateItems(
        for observer: NSFileProviderEnumerationObserver,
        startingAt page: NSFileProviderPage
    ) {
        let snapshot = (try? HelperStorage().readSnapshot()) ?? HelperSnapshot(serverURL: "", helperID: "")
        let items: [NSFileProviderItem]

        if containerIdentifier == .rootContainer {
            items = snapshot.projects
                .filter(\.enabled)
                .map { RoughdashProjectProviderItem(project: $0) }
        } else if let projectID = RoughdashFileProviderIdentifiers.projectID(from: containerIdentifier) {
            items = snapshot.items
                .filter { $0.projectId == projectID && $0.parentId == "root" && !$0.tombstoned }
                .map { RoughdashProviderItem(item: $0) }
        } else {
            let parentID = containerIdentifier.rawValue
            items = snapshot.items
                .filter { $0.parentId == parentID && !$0.tombstoned }
                .map { RoughdashProviderItem(item: $0) }
        }

        observer.didEnumerate(items)
        observer.finishEnumerating(upTo: nil)
    }

    func enumerateChanges(
        for observer: NSFileProviderChangeObserver,
        from syncAnchor: NSFileProviderSyncAnchor
    ) {
        // The real implementation streams Roughdash revisions after the anchor.
        observer.finishEnumeratingChanges(upTo: syncAnchor, moreComing: false)
    }

    func currentSyncAnchor(completionHandler: @escaping (NSFileProviderSyncAnchor?) -> Void) {
        completionHandler(NSFileProviderSyncAnchor(Data("roughdash-bootstrap".utf8)))
    }
}
