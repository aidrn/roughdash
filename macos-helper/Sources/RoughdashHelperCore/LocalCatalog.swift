import Foundation

public actor LocalCatalog {
    private var projects: [String: SyncProject] = [:]
    private var items: [String: SyncItem] = [:]
    private var dirtyItemIDs: Set<String> = []

    public init() {}

    public func replaceProjects(_ values: [SyncProject]) {
        projects = Dictionary(uniqueKeysWithValues: values.map { ($0.id, $0) })
    }

    public func replaceItems(_ values: [SyncItem]) {
        for item in values {
            items[item.id] = item
            if item.dirty {
                dirtyItemIDs.insert(item.id)
            }
        }
    }

    public func item(id: String) -> SyncItem? {
        items[id]
    }

    public func markDirty(itemID: String) {
        dirtyItemIDs.insert(itemID)
    }

    public func canEvict(itemID: String) -> Bool {
        !dirtyItemIDs.contains(itemID)
    }
}
