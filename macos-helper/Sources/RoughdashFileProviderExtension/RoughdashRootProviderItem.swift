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
    var capabilities: NSFileProviderItemCapabilities { [.allowsReading] }
}
