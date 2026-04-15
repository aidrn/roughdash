import FileProvider
import Foundation
import UniformTypeIdentifiers

public final class RoughdashProviderItem: NSObject, NSFileProviderItem {
    private let item: SyncItem
    private static let allowsEvictingCapability = NSFileProviderItemCapabilities(rawValue: 1 << 6)

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

    public var itemVersion: NSFileProviderItemVersion {
        NSFileProviderItemVersion(
            contentVersion: Self.versionComponent(primary: item.contentHash, fallback: "content:\(item.revision)"),
            metadataVersion: Self.versionComponent(primary: item.metadataHash, fallback: "metadata:\(item.revision)")
        )
    }

    public var capabilities: NSFileProviderItemCapabilities {
        guard !item.tombstoned else {
            return []
        }

        var capabilities: NSFileProviderItemCapabilities = [.allowsReading, .allowsWriting, .allowsDeleting, .allowsReparenting, .allowsRenaming]
        if item.kind == "file" && !item.dirty {
            capabilities.insert(Self.allowsEvictingCapability)
        }
        return capabilities
    }

    public var contentPolicy: NSFileProviderContentPolicy {
        item.dirty ? .downloadEagerlyAndKeepDownloaded : .downloadLazily
    }

    public var isUploaded: Bool {
        !item.dirty
    }

    public var isUploading: Bool {
        false
    }

    public var isMostRecentVersionDownloaded: Bool {
        true
    }

    private static func versionComponent(primary: String, fallback: String) -> Data {
        let selected = primary.isEmpty ? fallback : primary
        let data = Data(selected.utf8)
        guard !data.isEmpty else {
            return Data("0".utf8)
        }
        return data.count <= 128 ? data : Data(data.prefix(128))
    }
}
