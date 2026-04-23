import CryptoKit
import Foundation
import OSLog

public actor TransferClient {
    private let api: RoughdashAPIClient
    private let logger = Logger(subsystem: "com.roughdash.helper", category: "transfer-client")

    public init(api: RoughdashAPIClient) {
        self.api = api
    }

    public func uploadFile(projectID: String, deviceID: String, itemID: String?, relativePath: String, baseRevision: Int64, fileURL: URL) async throws -> CompletedTransfer {
        logger.info("Preparing Roughdash upload for \(relativePath, privacy: .public): \(Self.localPathState(fileURL), privacy: .public)")

        let attributes: [FileAttributeKey: Any]
        do {
            attributes = try FileManager.default.attributesOfItem(atPath: fileURL.path)
        } catch {
            logger.error("Could not read local file attributes for \(relativePath, privacy: .public): \(Self.describe(error), privacy: .public) | \(Self.localPathState(fileURL), privacy: .public)")
            throw error
        }
        let size = (attributes[.size] as? NSNumber)?.int64Value ?? 0
        let handle: FileHandle
        do {
            handle = try CoordinatedFileReader.readableHandle(at: fileURL)
        } catch {
            logger.error("Could not open coordinated local upload file \(relativePath, privacy: .public): \(Self.describe(error), privacy: .public) | \(Self.localPathState(fileURL), privacy: .public)")
            throw error
        }
        defer { try? handle.close() }

        let digest: String
        do {
            digest = try CoordinatedFileReader.sha256Hex(from: handle)
        } catch {
            logger.error("Could not hash local upload file \(relativePath, privacy: .public): \(Self.describe(error), privacy: .public) | \(Self.localPathState(fileURL), privacy: .public)")
            throw error
        }
        let transfer = try await api.createTransfer(TransferSession(
            id: nil,
            direction: "upload",
            projectId: projectID,
            deviceId: deviceID,
            itemId: itemID,
            relativePath: relativePath,
            baseRevision: baseRevision,
            size: size,
            chunkSize: 8 * 1024 * 1024,
            sha256: digest,
            createdAt: nil,
            expiresAt: nil
        ))

        guard let transferID = transfer.id else {
            throw APIError.invalidResponse
        }
        do {
            try handle.seek(toOffset: 0)
        } catch {
            logger.error("Could not rewind local upload file \(relativePath, privacy: .public): \(Self.describe(error), privacy: .public) | \(Self.localPathState(fileURL), privacy: .public)")
            throw error
        }
        var index: Int64 = 0
        while true {
            let data = try handle.read(upToCount: Int(transfer.chunkSize)) ?? Data()
            if data.isEmpty {
                break
            }
            _ = try await api.uploadChunk(transferID: transferID, index: index, data: data)
            index += 1
        }
        return try await api.completeTransfer(transferID: transferID)
    }

    private static func localPathState(_ fileURL: URL) -> String {
        let fileManager = FileManager.default
        var isDirectory = ObjCBool(false)
        let exists = fileManager.fileExists(atPath: fileURL.path, isDirectory: &isDirectory)
        let readable = fileManager.isReadableFile(atPath: fileURL.path)
        let writable = fileManager.isWritableFile(atPath: fileURL.path)
        let resolvedPath = fileURL.resolvingSymlinksInPath().path
        let attributes = try? fileManager.attributesOfItem(atPath: fileURL.path)
        let size = (attributes?[.size] as? NSNumber)?.stringValue ?? "nil"
        let type = (attributes?[.type] as? FileAttributeType)?.rawValue ?? "nil"
        let modificationDate = (attributes?[.modificationDate] as? Date)?.description ?? "nil"
        return "path=\(fileURL.path) resolved=\(resolvedPath) exists=\(exists) isDirectory=\(isDirectory.boolValue) readable=\(readable) writable=\(writable) size=\(size) type=\(type) mtime=\(modificationDate)"
    }

    private static func describe(_ error: Error) -> String {
        let nsError = error as NSError
        return "\(error.localizedDescription) (domain=\(nsError.domain), code=\(nsError.code))"
    }
}
