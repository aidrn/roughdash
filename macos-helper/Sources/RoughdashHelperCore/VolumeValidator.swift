import Foundation
import FileProvider

public struct VolumeValidator {
    public init() {}

    public func validateDedicatedSSD(at url: URL) throws -> VolumeCheck {
        let keys: Set<URLResourceKey> = [
            .volumeUUIDStringKey,
            .volumeIsReadOnlyKey,
            .volumeIsLocalKey,
            .volumeIsEncryptedKey,
            .volumeLocalizedFormatDescriptionKey
        ]
        let values = try url.resourceValues(forKeys: keys)
        let uuid = values.volumeUUIDString ?? ""
        let format = values.volumeLocalizedFormatDescription?.lowercased() ?? ""
        let isReadOnly = values.volumeIsReadOnly ?? true
        let isLocal = values.volumeIsLocal ?? false
        let isEncrypted = values.volumeIsEncrypted ?? false

        if uuid.isEmpty {
            return VolumeCheck(url: url, volumeUUID: "", isSupported: false, reason: "The selected volume does not expose a stable UUID.")
        }
        if isReadOnly {
            return VolumeCheck(url: url, volumeUUID: uuid, isSupported: false, reason: "The selected volume is read-only.")
        }
        if !isLocal {
            return VolumeCheck(url: url, volumeUUID: uuid, isSupported: false, reason: "The selected volume is not local storage.")
        }
        if !isEncrypted {
            return VolumeCheck(url: url, volumeUUID: uuid, isSupported: false, reason: "The selected volume must be APFS encrypted.")
        }
        if !format.contains("apfs") {
            return VolumeCheck(url: url, volumeUUID: uuid, isSupported: false, reason: "The selected volume must be formatted as APFS.")
        }
        if let ownershipEnabled = Self.globalPermissionsEnabled(at: url), !ownershipEnabled {
            return VolumeCheck(
                url: url,
                volumeUUID: uuid,
                isSupported: false,
                reason: "The selected volume has file ownership disabled. Run `sudo diskutil enableOwnership \(url.path)` and register the Roughdash domain again."
            )
        }
        if #available(macOS 15.0, *) {
            switch try NSFileProviderManager.checkDomainsCanBeStoredOnVolume(at: url) {
            case .eligible:
                break
            case .ineligible(let reason):
                return VolumeCheck(
                    url: url,
                    volumeUUID: uuid,
                    isSupported: false,
                    reason: "The selected volume is not eligible for File Provider domains: \(FileProviderDomainRegistrar.describeUnsupportedReason(reason))."
                )
            @unknown default:
                return VolumeCheck(
                    url: url,
                    volumeUUID: uuid,
                    isSupported: false,
                    reason: "The selected volume returned an unknown File Provider eligibility result."
                )
            }
        }
        return VolumeCheck(url: url, volumeUUID: uuid, isSupported: true, reason: nil)
    }

    private static func globalPermissionsEnabled(at url: URL) -> Bool? {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/sbin/diskutil")
        process.arguments = ["info", "-plist", url.path]

        let pipe = Pipe()
        process.standardOutput = pipe
        process.standardError = Pipe()

        do {
            try process.run()
        } catch {
            return nil
        }
        process.waitUntilExit()
        guard process.terminationStatus == 0 else {
            return nil
        }

        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        guard let plist = try? PropertyListSerialization.propertyList(from: data, options: [], format: nil),
              let values = plist as? [String: Any] else {
            return nil
        }
        return values["GlobalPermissionsEnabled"] as? Bool
    }
}
