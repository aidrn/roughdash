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
}
