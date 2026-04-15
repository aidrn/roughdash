import FileProvider
import Foundation
import OSLog
import RoughdashHelperCore

private final class FileProviderBundleAnchor: NSObject {}

enum RoughdashExtensionStorage {
    private static let logger = Logger(subsystem: "com.roughdash.helper", category: "file-provider-storage")
    private static let fallbackAppGroupIdentifier = "group.com.aiden.roughdash.helper"

    static func readSnapshot(for domain: NSFileProviderDomain) -> HelperSnapshot {
        if let snapshot = readStateDirectorySnapshot(for: domain) {
            return snapshot
        }

        do {
            let storage = try HelperStorage(appGroupIdentifier: appGroupIdentifier())
            let snapshot = try storage.readSnapshot() ?? HelperSnapshot(serverURL: "", helperID: "")
            logger.info("Read fallback App Group snapshot from \(storage.containerURL.path, privacy: .public): \(snapshot.projects.count, privacy: .public) projects, \(snapshot.items.count, privacy: .public) items")
            return snapshot
        } catch {
            logger.error("Could not read fallback App Group snapshot: \(describe(error), privacy: .public)")
            return HelperSnapshot(serverURL: "", helperID: "")
        }
    }

    private static func readStateDirectorySnapshot(for domain: NSFileProviderDomain) -> HelperSnapshot? {
        guard #available(macOS 15.0, *) else {
            return nil
        }

        guard let manager = NSFileProviderManager(for: domain) else {
            logger.error("Could not create File Provider manager for snapshot domain \(domain.identifier.rawValue, privacy: .public)")
            return nil
        }

        do {
            let stateDirectoryURL = try manager.stateDirectoryURL()
            let didAccessStateDirectory = stateDirectoryURL.startAccessingSecurityScopedResource()
            defer {
                if didAccessStateDirectory {
                    stateDirectoryURL.stopAccessingSecurityScopedResource()
                }
            }

            let storage = HelperStorage(fileProviderStateDirectoryURL: stateDirectoryURL)
            guard let snapshot = try storage.readSnapshot() else {
                logger.info("No File Provider state snapshot found in \(stateDirectoryURL.path, privacy: .public)")
                return nil
            }
            logger.info("Read File Provider state snapshot from \(stateDirectoryURL.path, privacy: .public): \(snapshot.projects.count, privacy: .public) projects, \(snapshot.items.count, privacy: .public) items")
            return snapshot
        } catch {
            logger.error("Could not read File Provider state snapshot for \(domain.identifier.rawValue, privacy: .public): \(describe(error), privacy: .public)")
            return nil
        }
    }

    private static func appGroupIdentifier() -> String {
        let bundleCandidates = [
            Bundle.main,
            Bundle(for: FileProviderBundleAnchor.self)
        ]

        for bundle in bundleCandidates {
            if let identifier = HelperStorage.appGroupIdentifier(in: bundle), !identifier.isEmpty {
                return identifier
            }
        }

        return fallbackAppGroupIdentifier
    }

    private static func describe(_ error: Error) -> String {
        let nsError = error as NSError
        var parts = [
            error.localizedDescription,
            "domain=\(nsError.domain)",
            "code=\(nsError.code)"
        ]
        if !nsError.userInfo.isEmpty {
            let details = nsError.userInfo
                .map { "\($0.key)=\($0.value)" }
                .sorted()
                .joined(separator: ", ")
            parts.append("userInfo={\(details)}")
        }
        return parts.joined(separator: " | ")
    }
}
