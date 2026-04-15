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
        // The real implementation lists children from the local catalog, refreshing
        // Roughdash project roots when enumerating the root container.
        observer.didEnumerate([])
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
