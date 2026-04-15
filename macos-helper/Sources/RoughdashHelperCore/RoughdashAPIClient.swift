import Foundation

public actor RoughdashAPIClient {
    private let baseURL: URL
    private let helperID: String?
    private let helperToken: String?
    private let session: URLSession
    private let decoder: JSONDecoder
    private let encoder: JSONEncoder

    public init(baseURL: URL, helperID: String? = nil, helperToken: String? = nil, session: URLSession = .shared) {
        self.baseURL = baseURL
        self.helperID = helperID
        self.helperToken = helperToken
        self.session = session
        self.decoder = JSONDecoder()
        self.decoder.dateDecodingStrategy = .iso8601
        self.encoder = JSONEncoder()
        self.encoder.dateEncodingStrategy = .iso8601
    }

    public func listProjects() async throws -> [SyncProject] {
        let response: ProjectsResponse = try await request("GET", "/api/sync/projects")
        return response.projects
    }

    public func registerDevice(_ device: SyncDevice) async throws -> SyncDevice {
        let response: DeviceResponse = try await request("POST", "/api/sync/devices", body: device)
        return response.device
    }

    public func acquireLease(deviceID: String, volumeUUID: String, force: Bool = false) async throws -> SyncLease {
        let body = LeaseRequest(ssdVolumeUuid: volumeUUID, ttlSeconds: 300, force: force)
        let response: LeaseResponse = try await request("POST", "/api/sync/devices/\(deviceID)/lease", body: body)
        return response.lease
    }

    public func listItems(projectID: String, parentID: String = "root") async throws -> [SyncItem] {
        let response: ItemsResponse = try await request("GET", "/api/sync/projects/\(projectID)/items?parentId=\(parentID)")
        return response.items
    }

    public func upsertPin(_ pin: SyncPin) async throws -> SyncPin {
        let response: PinResponse = try await request("POST", "/api/sync/pins", body: pin)
        return response.pin
    }

    public func listConflicts() async throws -> [SyncConflict] {
        let response: ConflictsResponse = try await request("GET", "/api/sync/conflicts?status=open")
        return response.conflicts
    }

    public func createTransfer(_ transfer: TransferSession) async throws -> TransferSession {
        let response: TransferResponse = try await request("POST", "/api/sync/transfers", body: transfer)
        return response.transfer
    }

    public func uploadChunk(transferID: String, index: Int64, data: Data) async throws -> TransferChunkReceipt {
        var request = try urlRequest("PUT", "/api/sync/transfers/\(transferID)/chunks/\(index)")
        request.httpBody = data
        let (body, response) = try await session.data(for: request)
        try validate(response: response, body: body)
        return try decoder.decode(ChunkResponse.self, from: body).chunk
    }

    public func completeTransfer(transferID: String) async throws -> TransferSession {
        let response: TransferResponse = try await request("POST", "/api/sync/transfers/\(transferID)/complete")
        return response.transfer
    }

    private func request<Response: Decodable>(_ method: String, _ path: String) async throws -> Response {
        let request = try urlRequest(method, path)
        let (body, response) = try await session.data(for: request)
        try validate(response: response, body: body)
        return try decoder.decode(Response.self, from: body)
    }

    private func request<RequestBody: Encodable, Response: Decodable>(_ method: String, _ path: String, body: RequestBody) async throws -> Response {
        var request = try urlRequest(method, path)
        request.httpBody = try encoder.encode(body)
        let (responseBody, response) = try await session.data(for: request)
        try validate(response: response, body: responseBody)
        return try decoder.decode(Response.self, from: responseBody)
    }

    private func urlRequest(_ method: String, _ path: String) throws -> URLRequest {
        guard let url = URL(string: path, relativeTo: baseURL) else {
            throw APIError.invalidURL(path)
        }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        if let helperID {
            request.setValue(helperID, forHTTPHeaderField: "X-Roughdash-Helper-ID")
        }
        if let helperToken {
            request.setValue("Bearer \(helperToken)", forHTTPHeaderField: "Authorization")
        }
        return request
    }

    private func validate(response: URLResponse, body: Data) throws {
        guard let http = response as? HTTPURLResponse else {
            throw APIError.invalidResponse
        }
        guard (200..<300).contains(http.statusCode) else {
            let message = (try? decoder.decode(ErrorResponse.self, from: body).error) ?? HTTPURLResponse.localizedString(forStatusCode: http.statusCode)
            throw APIError.server(status: http.statusCode, message: message)
        }
    }
}

public enum APIError: Error, LocalizedError {
    case invalidURL(String)
    case invalidResponse
    case server(status: Int, message: String)

    public var errorDescription: String? {
        switch self {
        case .invalidURL(let value): return "Invalid Roughdash URL: \(value)"
        case .invalidResponse: return "Invalid Roughdash response."
        case .server(_, let message): return message
        }
    }
}

private struct ProjectsResponse: Decodable { var projects: [SyncProject] }
private struct DeviceResponse: Decodable { var device: SyncDevice }
private struct LeaseRequest: Encodable { var ssdVolumeUuid: String; var ttlSeconds: Int; var force: Bool }
private struct LeaseResponse: Decodable { var lease: SyncLease }
private struct ItemsResponse: Decodable { var items: [SyncItem] }
private struct PinResponse: Decodable { var pin: SyncPin }
private struct ConflictsResponse: Decodable { var conflicts: [SyncConflict] }
private struct TransferResponse: Decodable { var transfer: TransferSession }
private struct ChunkResponse: Decodable { var chunk: TransferChunkReceipt }
private struct ErrorResponse: Decodable { var error: String }
