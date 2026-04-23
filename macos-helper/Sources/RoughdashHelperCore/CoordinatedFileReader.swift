import CryptoKit
import Foundation

public enum CoordinatedFileReader {
    public static func readableHandle(at fileURL: URL) throws -> FileHandle {
        var coordinatedHandle: FileHandle?
        var coordinationError: NSError?
        var openError: Error?

        let coordinator = NSFileCoordinator(filePresenter: nil)
        coordinator.coordinate(readingItemAt: fileURL, options: [.forUploading], error: &coordinationError) { coordinatedURL in
            do {
                coordinatedHandle = try FileHandle(forReadingFrom: coordinatedURL)
            } catch {
                openError = error
            }
        }

        if let coordinatedHandle {
            return coordinatedHandle
        }

        do {
            return try FileHandle(forReadingFrom: fileURL)
        } catch {
            if let coordinationError {
                throw coordinationError
            }
            if let openError {
                throw openError
            }
            throw error
        }
    }

    public static func sha256Hex(at fileURL: URL) throws -> String {
        let handle = try readableHandle(at: fileURL)
        defer { try? handle.close() }
        return try sha256Hex(from: handle)
    }

    public static func sha256Hex(from handle: FileHandle) throws -> String {
        try handle.seek(toOffset: 0)

        var hasher = SHA256()
        while true {
            let data = try handle.read(upToCount: 8 * 1024 * 1024) ?? Data()
            if data.isEmpty {
                break
            }
            hasher.update(data: data)
        }
        return hasher.finalize().map { String(format: "%02x", $0) }.joined()
    }
}
