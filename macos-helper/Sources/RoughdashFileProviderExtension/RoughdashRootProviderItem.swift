import FileProvider
import Foundation
import UniformTypeIdentifiers

final class RoughdashRootProviderItem: NSObject, NSFileProviderItem {
    let domain: NSFileProviderDomain

    init(domain: NSFileProviderDomain) {
        self.domain = domain
    }

    var itemIdentifier: NSFileProviderItemIdentifier { .rootContainer }
    var parentItemIdentifier: NSFileProviderItemIdentifier { .rootContainer }
    var filename: String { domain.displayName }
    var contentType: UTType { .folder }
    var itemVersion: NSFileProviderItemVersion {
        NSFileProviderItemVersion(
            contentVersion: Data("roughdash-root-v1".utf8),
            metadataVersion: Data("roughdash-root-v1".utf8)
        )
    }
    var capabilities: NSFileProviderItemCapabilities { [.allowsReading] }
}

final class RoughdashTrashProviderItem: NSObject, NSFileProviderItem {
    var itemIdentifier: NSFileProviderItemIdentifier { .trashContainer }
    var parentItemIdentifier: NSFileProviderItemIdentifier { .rootContainer }
    var filename: String { ".Trash" }
    var contentType: UTType { .folder }
    var itemVersion: NSFileProviderItemVersion {
        NSFileProviderItemVersion(
            contentVersion: Data("roughdash-trash-v1".utf8),
            metadataVersion: Data("roughdash-trash-v1".utf8)
        )
    }
    var capabilities: NSFileProviderItemCapabilities { [.allowsReading] }
}
