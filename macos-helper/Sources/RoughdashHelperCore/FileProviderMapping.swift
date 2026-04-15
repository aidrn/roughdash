import FileProvider
import Foundation
import UniformTypeIdentifiers

public final class RoughdashProviderItem: NSObject, NSFileProviderItem {
    private let item: SyncItem

    public init(item: SyncItem) {
        self.item = item
    }

    public var itemIdentifier: NSFileProviderItemIdentifier {
        NSFileProviderItemIdentifier(item.id)
    }

    public var parentItemIdentifier: NSFileProviderItemIdentifier {
        item.parentId == "root" ? RoughdashFileProviderIdentifiers.projectRootIdentifier(projectID: item.projectId) : NSFileProviderItemIdentifier(item.parentId)
    }

    public var filename: String {
        item.name
    }

    public var contentType: UTType {
        item.kind == "directory" ? .folder : .data
    }

    public var documentSize: NSNumber? {
        NSNumber(value: item.size)
    }

    public var contentModificationDate: Date? {
        item.modTime
    }

    public var capabilities: NSFileProviderItemCapabilities {
        item.tombstoned ? [] : [.allowsReading, .allowsWriting, .allowsDeleting, .allowsReparenting, .allowsRenaming]
    }
}
