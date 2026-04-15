import FileProvider
import Foundation
import UniformTypeIdentifiers

public enum RoughdashFileProviderIdentifiers {
    public static let projectPrefix = "roughdash-project:"

    public static func projectRootIdentifier(projectID: String) -> NSFileProviderItemIdentifier {
        NSFileProviderItemIdentifier(projectPrefix + projectID)
    }

    public static func projectID(from identifier: NSFileProviderItemIdentifier) -> String? {
        let rawValue = identifier.rawValue
        guard rawValue.hasPrefix(projectPrefix) else {
            return nil
        }
        return String(rawValue.dropFirst(projectPrefix.count))
    }
}

public final class RoughdashProjectProviderItem: NSObject, NSFileProviderItem {
    private let project: SyncProject

    public init(project: SyncProject) {
        self.project = project
    }

    public var itemIdentifier: NSFileProviderItemIdentifier {
        RoughdashFileProviderIdentifiers.projectRootIdentifier(projectID: project.id)
    }

    public var parentItemIdentifier: NSFileProviderItemIdentifier {
        .rootContainer
    }

    public var filename: String {
        project.name
    }

    public var contentType: UTType {
        .folder
    }

    public var contentModificationDate: Date? {
        project.updatedAt
    }

    public var capabilities: NSFileProviderItemCapabilities {
        project.enabled ? [.allowsReading, .allowsWriting] : [.allowsReading]
    }
}
