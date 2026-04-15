import CryptoKit
import Foundation

public actor TransferClient {
    private let api: RoughdashAPIClient

    public init(api: RoughdashAPIClient) {
        self.api = api
    }

    public func uploadFile(projectID: String, deviceID: String, itemID: String?, relativePath: String, baseRevision: Int64, fileURL: URL) async throws -> TransferSession {
        let attributes = try FileManager.default.attributesOfItem(atPath: fileURL.path)
        let size = (attributes[.size] as? NSNumber)?.int64Value ?? 0
        let digest = try SHA256.hash(data: Data(contentsOf: fileURL)).map { String(format: "%02x", $0) }.joined()
        var transfer = try await api.createTransfer(TransferSession(
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
        let handle = try FileHandle(forReadingFrom: fileURL)
        defer { try? handle.close() }
        var index: Int64 = 0
        while true {
            let data = try handle.read(upToCount: Int(transfer.chunkSize)) ?? Data()
            if data.isEmpty {
                break
            }
            _ = try await api.uploadChunk(transferID: transferID, index: index, data: data)
            index += 1
        }
        transfer = try await api.completeTransfer(transferID: transferID)
        return transfer
    }
}
