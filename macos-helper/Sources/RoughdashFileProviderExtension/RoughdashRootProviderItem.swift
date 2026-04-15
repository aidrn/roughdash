import FileProvider
import Foundation
import UniformTypeIdentifiers

struct RoughdashRootProviderItem: NSFileProviderItem {
    let domain: NSFileProviderDomain

    var itemIdentifier: NSFileProviderItemIdentifier { .rootContainer }
    var parentItemIdentifier: NSFileProviderItemIdentifier { .rootContainer }
    var filename: String { domain.displayName }
    var contentType: UTType { .folder }
    var capabilities: NSFileProviderItemCapabilities { [.allowsReading] }
}
