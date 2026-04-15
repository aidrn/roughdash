import FileProvider
import Foundation

public struct RegisteredFileProviderDomain: Sendable {
    public var identifier: String
    public var displayName: String
    public var volumeUUID: String
    public var isExternalVolumeDomain: Bool

    public init(identifier: String, displayName: String, volumeUUID: String, isExternalVolumeDomain: Bool) {
        self.identifier = identifier
        self.displayName = displayName
        self.volumeUUID = volumeUUID
        self.isExternalVolumeDomain = isExternalVolumeDomain
    }
}

public enum FileProviderDomainRegistrarError: Error, LocalizedError {
    case externalVolumeDomainsRequireMacOS15
    case volumeIneligible(String)

    public var errorDescription: String? {
        switch self {
        case .externalVolumeDomainsRequireMacOS15:
            return "File Provider domains on external volumes require macOS 15 or newer."
        case .volumeIneligible(let reason):
            return "The selected volume is not eligible for File Provider domains: \(reason)."
        }
    }
}

public struct FileProviderDomainRegistrar: Sendable {
    private let displayName = "Roughdash"

    public init() {}

    public func registerRoughdashDomain(
        on volumeURL: URL,
        volumeUUID: String,
        serverURL: String,
        helperID: String
    ) async throws -> RegisteredFileProviderDomain {
        guard #available(macOS 15.0, *) else {
            throw FileProviderDomainRegistrarError.externalVolumeDomainsRequireMacOS15
        }

        try ensureEligible(volumeURL)

        if let existing = try await existingRoughdashDomain(volumeUUID: volumeUUID) {
            return existing
        }

        let externalDomain = NSFileProviderDomain(
            displayName: displayName,
            userInfo: [
                "roughdash": "true",
                "volumeUUID": volumeUUID,
                "serverURL": serverURL,
                "helperID": helperID
            ],
            volumeURL: volumeURL
        )
        externalDomain.supportsSyncingTrash = true

        do {
            try await add(externalDomain)
            return RegisteredFileProviderDomain(
                identifier: externalDomain.identifier.rawValue,
                displayName: externalDomain.displayName,
                volumeUUID: volumeUUID,
                isExternalVolumeDomain: true
            )
        } catch {
            guard Self.isFeatureUnsupported(error) else {
                throw error
            }
            return try await registerStandardDomain(volumeUUID: volumeUUID, serverURL: serverURL, helperID: helperID)
        }
    }

    public func removeRoughdashDomains(volumeUUID: String? = nil) async throws -> Int {
        guard #available(macOS 15.0, *) else {
            throw FileProviderDomainRegistrarError.externalVolumeDomainsRequireMacOS15
        }

        let domains = try await domains().domains
        var removedCount = 0
        for domain in domains where isRoughdashDomain(domain, volumeUUID: volumeUUID) {
            try await remove(domain)
            removedCount += 1
        }
        return removedCount
    }

    @available(macOS 15.0, *)
    private func ensureEligible(_ volumeURL: URL) throws {
        let result = try NSFileProviderManager.checkDomainsCanBeStoredOnVolume(at: volumeURL)
        switch result {
        case .eligible:
            return
        case .ineligible(let reason):
            throw FileProviderDomainRegistrarError.volumeIneligible(Self.describeUnsupportedReason(reason))
        @unknown default:
            throw FileProviderDomainRegistrarError.volumeIneligible("unknown")
        }
    }

    @available(macOS 15.0, *)
    private func existingRoughdashDomain(volumeUUID: String) async throws -> RegisteredFileProviderDomain? {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<RegisteredFileProviderDomain?, Error>) in
            NSFileProviderManager.getDomainsWithCompletionHandler { domains, error in
                if let error {
                    continuation.resume(throwing: error)
                    return
                }

                let match = domains.first { domain in
                    guard domain.displayName == displayName else {
                        return false
                    }
                    if let userInfoVolumeUUID = domain.userInfo?["volumeUUID"] as? String {
                        return userInfoVolumeUUID == volumeUUID
                    }
                    return domain.volumeUUID?.uuidString.caseInsensitiveCompare(volumeUUID) == .orderedSame
                }

                continuation.resume(returning: match.map {
                    RegisteredFileProviderDomain(
                        identifier: $0.identifier.rawValue,
                        displayName: $0.displayName,
                        volumeUUID: volumeUUID,
                        isExternalVolumeDomain: $0.volumeUUID != nil
                    )
                })
            }
        }
    }

    @available(macOS 15.0, *)
    private func registerStandardDomain(volumeUUID: String, serverURL: String, helperID: String) async throws -> RegisteredFileProviderDomain {
        let identifier = NSFileProviderDomainIdentifier("roughdash-\(volumeUUID.lowercased())")
        let domain = NSFileProviderDomain(identifier: identifier, displayName: displayName)
        domain.supportsSyncingTrash = true
        domain.userInfo = [
            "roughdash": "true",
            "volumeUUID": volumeUUID,
            "serverURL": serverURL,
            "helperID": helperID,
            "fallback": "standard-domain"
        ]

        try await add(domain)
        return RegisteredFileProviderDomain(
            identifier: domain.identifier.rawValue,
            displayName: domain.displayName,
            volumeUUID: volumeUUID,
            isExternalVolumeDomain: false
        )
    }

    @available(macOS 15.0, *)
    private func add(_ domain: NSFileProviderDomain) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            NSFileProviderManager.add(domain) { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume()
                }
            }
        }
    }

    @available(macOS 15.0, *)
    private func domains() async throws -> FileProviderDomainsBox {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<FileProviderDomainsBox, Error>) in
            NSFileProviderManager.getDomainsWithCompletionHandler { domains, error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume(returning: FileProviderDomainsBox(domains))
                }
            }
        }
    }

    @available(macOS 15.0, *)
    private func remove(_ domain: NSFileProviderDomain) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            NSFileProviderManager.remove(domain) { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume()
                }
            }
        }
    }

    @available(macOS 15.0, *)
    private func isRoughdashDomain(_ domain: NSFileProviderDomain, volumeUUID: String?) -> Bool {
        let isRoughdash = domain.displayName == displayName
            || domain.identifier.rawValue.hasPrefix("roughdash-")
            || (domain.userInfo?["roughdash"] as? String) == "true"
        guard isRoughdash else {
            return false
        }

        guard let volumeUUID, !volumeUUID.isEmpty else {
            return true
        }

        if let userInfoVolumeUUID = domain.userInfo?["volumeUUID"] as? String {
            return userInfoVolumeUUID.caseInsensitiveCompare(volumeUUID) == .orderedSame
        }
        return domain.volumeUUID?.uuidString.caseInsensitiveCompare(volumeUUID) == .orderedSame
    }

    @available(macOS 15.0, *)
    public static func describeUnsupportedReason(_ reason: NSFileProviderVolumeUnsupportedReason) -> String {
        var values: [String] = []
        if reason.contains(.nonAPFS) { values.append("non-APFS") }
        if reason.contains(.nonEncrypted) { values.append("not encrypted") }
        if reason.contains(.readOnly) { values.append("read-only") }
        if reason.contains(.network) { values.append("network volume") }
        if reason.contains(.quarantined) { values.append("quarantined") }
        if reason.contains(.unknown) { values.append("unknown") }
        return values.isEmpty ? "unknown" : values.joined(separator: ", ")
    }

    private static func isFeatureUnsupported(_ error: Error) -> Bool {
        let nsError = error as NSError
        return nsError.domain == NSCocoaErrorDomain && nsError.code == NSFeatureUnsupportedError
    }
}

private final class FileProviderDomainsBox: @unchecked Sendable {
    let domains: [NSFileProviderDomain]

    init(_ domains: [NSFileProviderDomain]) {
        self.domains = domains
    }
}
