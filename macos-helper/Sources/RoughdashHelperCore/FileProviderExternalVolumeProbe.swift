import FileProvider
import Foundation

public struct FileProviderExternalVolumeProbeReport: Codable, Sendable {
    public enum Outcome: String, Codable, Sendable {
        case missingVolumeSelection
        case volumeIneligible
        case addFailed
        case cleanupFailed
        case succeeded
    }

    public struct DomainSnapshot: Codable, Sendable {
        public var identifier: String
        public var displayName: String
        public var volumeUUID: String?
        public var userInfo: [String: String]

        public init(identifier: String, displayName: String, volumeUUID: String?, userInfo: [String: String]) {
            self.identifier = identifier
            self.displayName = displayName
            self.volumeUUID = volumeUUID
            self.userInfo = userInfo
        }
    }

    public struct ErrorSnapshot: Codable, Sendable {
        public var domain: String
        public var code: Int
        public var description: String
        public var userInfo: [String: String]

        public init(domain: String, code: Int, description: String, userInfo: [String: String]) {
            self.domain = domain
            self.code = code
            self.description = description
            self.userInfo = userInfo
        }
    }

    public var createdAt: Date
    public var osVersion: String
    public var appBundleIdentifier: String?
    public var appGroupIdentifier: String?
    public var providerBundleIdentifier: String?
    public var selectedVolumePath: String?
    public var volumeUUID: String?
    public var eligibility: String
    public var ineligibilityReason: String?
    public var outcome: Outcome
    public var probeDomainIdentifier: String?
    public var domainsBefore: [DomainSnapshot]
    public var domainsAfterAdd: [DomainSnapshot]
    public var domainsAfterCleanup: [DomainSnapshot]
    public var error: ErrorSnapshot?

    public init(
        createdAt: Date = Date(),
        osVersion: String,
        appBundleIdentifier: String?,
        appGroupIdentifier: String?,
        providerBundleIdentifier: String?,
        selectedVolumePath: String?,
        volumeUUID: String?,
        eligibility: String,
        ineligibilityReason: String?,
        outcome: Outcome,
        probeDomainIdentifier: String?,
        domainsBefore: [DomainSnapshot],
        domainsAfterAdd: [DomainSnapshot],
        domainsAfterCleanup: [DomainSnapshot],
        error: ErrorSnapshot?
    ) {
        self.createdAt = createdAt
        self.osVersion = osVersion
        self.appBundleIdentifier = appBundleIdentifier
        self.appGroupIdentifier = appGroupIdentifier
        self.providerBundleIdentifier = providerBundleIdentifier
        self.selectedVolumePath = selectedVolumePath
        self.volumeUUID = volumeUUID
        self.eligibility = eligibility
        self.ineligibilityReason = ineligibilityReason
        self.outcome = outcome
        self.probeDomainIdentifier = probeDomainIdentifier
        self.domainsBefore = domainsBefore
        self.domainsAfterAdd = domainsAfterAdd
        self.domainsAfterCleanup = domainsAfterCleanup
        self.error = error
    }

    public var summary: String {
        var parts = ["Probe \(outcome.rawValue)"]
        if let selectedVolumePath {
            parts.append("volume=\(selectedVolumePath)")
        }
        parts.append("eligibility=\(eligibility)")
        if let ineligibilityReason, !ineligibilityReason.isEmpty {
            parts.append("reason=\(ineligibilityReason)")
        }
        if let error {
            parts.append("error=\(error.description) [\(error.domain):\(error.code)]")
        }
        return parts.joined(separator: " | ")
    }
}

public struct FileProviderExternalVolumeProbe: Sendable {
    private let displayName = "Roughdash Probe"

    public init() {}

    public func run(on volumeURL: URL?) async -> FileProviderExternalVolumeProbeReport {
        let bundle = Bundle.main
        let baseReport = BaseReport(
            osVersion: ProcessInfo.processInfo.operatingSystemVersionString,
            appBundleIdentifier: bundle.bundleIdentifier,
            appGroupIdentifier: HelperStorage.appGroupIdentifier(in: bundle),
            providerBundleIdentifier: Self.providerBundleIdentifier(in: bundle)
        )

        guard #available(macOS 15.0, *) else {
            return FileProviderExternalVolumeProbeReport(
                osVersion: baseReport.osVersion,
                appBundleIdentifier: baseReport.appBundleIdentifier,
                appGroupIdentifier: baseReport.appGroupIdentifier,
                providerBundleIdentifier: baseReport.providerBundleIdentifier,
                selectedVolumePath: volumeURL?.path,
                volumeUUID: nil,
                eligibility: "unsupported-macos",
                ineligibilityReason: "File Provider domains on external volumes require macOS 15 or newer.",
                outcome: .volumeIneligible,
                probeDomainIdentifier: nil,
                domainsBefore: [],
                domainsAfterAdd: [],
                domainsAfterCleanup: [],
                error: nil
            )
        }

        let domainsBefore = (try? await domains().domains) ?? []

        guard let volumeURL else {
            return FileProviderExternalVolumeProbeReport(
                osVersion: baseReport.osVersion,
                appBundleIdentifier: baseReport.appBundleIdentifier,
                appGroupIdentifier: baseReport.appGroupIdentifier,
                providerBundleIdentifier: baseReport.providerBundleIdentifier,
                selectedVolumePath: nil,
                volumeUUID: nil,
                eligibility: "unknown",
                ineligibilityReason: "No SSD is currently selected.",
                outcome: .missingVolumeSelection,
                probeDomainIdentifier: nil,
                domainsBefore: domainsBefore.map(Self.domainSnapshot),
                domainsAfterAdd: [],
                domainsAfterCleanup: [],
                error: nil
            )
        }

        let volumeUUID = Self.volumeUUID(for: volumeURL)
        let eligibility = try? NSFileProviderManager.checkDomainsCanBeStoredOnVolume(at: volumeURL)
        switch eligibility {
        case .eligible?:
            break
        case .ineligible(let reason)?:
            return FileProviderExternalVolumeProbeReport(
                osVersion: baseReport.osVersion,
                appBundleIdentifier: baseReport.appBundleIdentifier,
                appGroupIdentifier: baseReport.appGroupIdentifier,
                providerBundleIdentifier: baseReport.providerBundleIdentifier,
                selectedVolumePath: volumeURL.path,
                volumeUUID: volumeUUID,
                eligibility: "ineligible",
                ineligibilityReason: FileProviderDomainRegistrar.describeUnsupportedReason(reason),
                outcome: .volumeIneligible,
                probeDomainIdentifier: nil,
                domainsBefore: domainsBefore.map(Self.domainSnapshot),
                domainsAfterAdd: [],
                domainsAfterCleanup: [],
                error: nil
            )
        case nil:
            return FileProviderExternalVolumeProbeReport(
                osVersion: baseReport.osVersion,
                appBundleIdentifier: baseReport.appBundleIdentifier,
                appGroupIdentifier: baseReport.appGroupIdentifier,
                providerBundleIdentifier: baseReport.providerBundleIdentifier,
                selectedVolumePath: volumeURL.path,
                volumeUUID: volumeUUID,
                eligibility: "check-failed",
                ineligibilityReason: "checkDomainsCanBeStoredOnVolume returned no result.",
                outcome: .volumeIneligible,
                probeDomainIdentifier: nil,
                domainsBefore: domainsBefore.map(Self.domainSnapshot),
                domainsAfterAdd: [],
                domainsAfterCleanup: [],
                error: nil
            )
        @unknown default:
            return FileProviderExternalVolumeProbeReport(
                osVersion: baseReport.osVersion,
                appBundleIdentifier: baseReport.appBundleIdentifier,
                appGroupIdentifier: baseReport.appGroupIdentifier,
                providerBundleIdentifier: baseReport.providerBundleIdentifier,
                selectedVolumePath: volumeURL.path,
                volumeUUID: volumeUUID,
                eligibility: "unknown",
                ineligibilityReason: "Received an unknown volume eligibility result.",
                outcome: .volumeIneligible,
                probeDomainIdentifier: nil,
                domainsBefore: domainsBefore.map(Self.domainSnapshot),
                domainsAfterAdd: [],
                domainsAfterCleanup: [],
                error: nil
            )
        }

        try? await removeExistingProbeDomains()

        let probeRunID = UUID().uuidString.lowercased()
        let domain = NSFileProviderDomain(
            displayName: displayName,
            userInfo: [
                "roughdash": "true",
                "roughdashProbe": "true",
                "probeRunID": probeRunID,
                "volumeUUID": volumeUUID ?? ""
            ],
            volumeURL: volumeURL
        )
        domain.supportsSyncingTrash = false

        do {
            try await add(domain)
        } catch {
            let afterAdd = (try? await domains().domains) ?? []
            return FileProviderExternalVolumeProbeReport(
                osVersion: baseReport.osVersion,
                appBundleIdentifier: baseReport.appBundleIdentifier,
                appGroupIdentifier: baseReport.appGroupIdentifier,
                providerBundleIdentifier: baseReport.providerBundleIdentifier,
                selectedVolumePath: volumeURL.path,
                volumeUUID: volumeUUID,
                eligibility: "eligible",
                ineligibilityReason: nil,
                outcome: .addFailed,
                probeDomainIdentifier: domain.identifier.rawValue,
                domainsBefore: domainsBefore.map(Self.domainSnapshot),
                domainsAfterAdd: afterAdd.map(Self.domainSnapshot),
                domainsAfterCleanup: [],
                error: Self.errorSnapshot(from: error)
            )
        }

        let afterAdd = (try? await domains().domains) ?? []

        do {
            try await remove(domain)
        } catch {
            let afterCleanup = (try? await domains().domains) ?? []
            return FileProviderExternalVolumeProbeReport(
                osVersion: baseReport.osVersion,
                appBundleIdentifier: baseReport.appBundleIdentifier,
                appGroupIdentifier: baseReport.appGroupIdentifier,
                providerBundleIdentifier: baseReport.providerBundleIdentifier,
                selectedVolumePath: volumeURL.path,
                volumeUUID: volumeUUID,
                eligibility: "eligible",
                ineligibilityReason: nil,
                outcome: .cleanupFailed,
                probeDomainIdentifier: domain.identifier.rawValue,
                domainsBefore: domainsBefore.map(Self.domainSnapshot),
                domainsAfterAdd: afterAdd.map(Self.domainSnapshot),
                domainsAfterCleanup: afterCleanup.map(Self.domainSnapshot),
                error: Self.errorSnapshot(from: error)
            )
        }

        let afterCleanup = (try? await domains().domains) ?? []
        return FileProviderExternalVolumeProbeReport(
            osVersion: baseReport.osVersion,
            appBundleIdentifier: baseReport.appBundleIdentifier,
            appGroupIdentifier: baseReport.appGroupIdentifier,
            providerBundleIdentifier: baseReport.providerBundleIdentifier,
            selectedVolumePath: volumeURL.path,
            volumeUUID: volumeUUID,
            eligibility: "eligible",
            ineligibilityReason: nil,
            outcome: .succeeded,
            probeDomainIdentifier: domain.identifier.rawValue,
            domainsBefore: domainsBefore.map(Self.domainSnapshot),
            domainsAfterAdd: afterAdd.map(Self.domainSnapshot),
            domainsAfterCleanup: afterCleanup.map(Self.domainSnapshot),
            error: nil
        )
    }

    @available(macOS 15.0, *)
    private func removeExistingProbeDomains() async throws {
        for domain in try await domains().domains where Self.isProbeDomain(domain) {
            try await remove(domain)
        }
    }

    @available(macOS 15.0, *)
    private func domains() async throws -> FileProviderDomainBox {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<FileProviderDomainBox, Error>) in
            NSFileProviderManager.getDomainsWithCompletionHandler { domains, error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume(returning: FileProviderDomainBox(domains))
                }
            }
        }
    }

    @available(macOS 15.0, *)
    private func add(_ domain: NSFileProviderDomain) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            NSFileProviderManager.add(domain) { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume(returning: ())
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
                    continuation.resume(returning: ())
                }
            }
        }
    }

    @available(macOS 15.0, *)
    private static func domainSnapshot(_ domain: NSFileProviderDomain) -> FileProviderExternalVolumeProbeReport.DomainSnapshot {
        let userInfo = (domain.userInfo ?? [:]).reduce(into: [String: String]()) { result, entry in
            result[String(describing: entry.key)] = String(describing: entry.value)
        }
        return FileProviderExternalVolumeProbeReport.DomainSnapshot(
            identifier: domain.identifier.rawValue,
            displayName: domain.displayName,
            volumeUUID: domain.volumeUUID?.uuidString,
            userInfo: userInfo
        )
    }

    private static func errorSnapshot(from error: Error) -> FileProviderExternalVolumeProbeReport.ErrorSnapshot {
        let nsError = error as NSError
        let userInfo = nsError.userInfo.reduce(into: [String: String]()) { result, entry in
            result[String(describing: entry.key)] = String(describing: entry.value)
        }
        return FileProviderExternalVolumeProbeReport.ErrorSnapshot(
            domain: nsError.domain,
            code: nsError.code,
            description: error.localizedDescription,
            userInfo: userInfo
        )
    }

    @available(macOS 15.0, *)
    private static func isProbeDomain(_ domain: NSFileProviderDomain) -> Bool {
        domain.displayName == "Roughdash Probe"
            || (domain.userInfo?["roughdashProbe"] as? String) == "true"
    }

    private static func providerBundleIdentifier(in bundle: Bundle) -> String? {
        guard let pluginsURL = bundle.builtInPlugInsURL,
              let pluginURLs = try? FileManager.default.contentsOfDirectory(at: pluginsURL, includingPropertiesForKeys: nil)
        else {
            return nil
        }

        for pluginURL in pluginURLs where pluginURL.pathExtension == "appex" {
            guard let pluginBundle = Bundle(url: pluginURL),
                  let extensionPointIdentifier = pluginBundle.infoDictionary?["NSExtension"] as? [String: Any],
                  let point = extensionPointIdentifier["NSExtensionPointIdentifier"] as? String,
                  point == "com.apple.fileprovider-nonui"
            else {
                continue
            }
            return pluginBundle.bundleIdentifier
        }

        return nil
    }

    private static func volumeUUID(for url: URL) -> String? {
        (try? url.resourceValues(forKeys: [.volumeUUIDStringKey]).volumeUUIDString)?.uppercased()
    }
}

private struct BaseReport {
    var osVersion: String
    var appBundleIdentifier: String?
    var appGroupIdentifier: String?
    var providerBundleIdentifier: String?
}

@available(macOS 15.0, *)
private final class FileProviderDomainBox: @unchecked Sendable {
    let domains: [NSFileProviderDomain]

    init(_ domains: [NSFileProviderDomain]) {
        self.domains = domains
    }
}
