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

    public var itemVersion: NSFileProviderItemVersion {
        let version = "project:\(project.id):\(Int64(project.updatedAt.timeIntervalSince1970))"
        return NSFileProviderItemVersion(
            contentVersion: Self.versionComponent(version),
            metadataVersion: Self.versionComponent(version)
        )
    }

    public var capabilities: NSFileProviderItemCapabilities {
        project.enabled ? [.allowsReading, .allowsWriting] : [.allowsReading]
    }

    private static func versionComponent(_ value: String) -> Data {
        let data = Data(value.utf8)
        guard !data.isEmpty else {
            return Data("0".utf8)
        }
        return data.count <= 128 ? data : Data(data.prefix(128))
    }
}
