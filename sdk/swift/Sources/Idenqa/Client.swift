import CryptoKit
import Foundation

public struct IdenqaClient: Sendable {
    private let baseURL: URL
    private let tokenStore: any CaptureTokenStore
    private let transport: any HTTPTransport
    private let realtime: any RealtimeTransport

    public init(baseURL: URL, tokenStore: any CaptureTokenStore, transport: any HTTPTransport = URLSessionTransport(), realtime: any RealtimeTransport = URLSessionRealtimeTransport()) throws {
        guard baseURL.scheme == "https", baseURL.user == nil, baseURL.password == nil else { throw IdenqaError.invalidConfiguration }
        self.baseURL = baseURL
        self.tokenStore = tokenStore
        self.transport = transport
        self.realtime = realtime
    }

    public func bootstrap(capabilities: CapabilityAdvertisement, identity: NativeBootstrapIdentity, now: Date = Date()) async throws -> CaptureSession {
        let response = try await bootstrapResponse(capabilities: capabilities, identity: identity, now: now)
        return try JSONDecoder.idenqa.decode(CaptureSession.self, from: response.body)
    }

    public func upload(to uploadURL: URL, contentType: String, body: Data) async throws {
        guard uploadURL.scheme == "https", !body.isEmpty else { throw IdenqaError.invalidConfiguration }
        let response = try await transport.send(TransportRequest(method: "PUT", url: uploadURL, headers: ["Content-Type": contentType, "Content-Length": String(body.count)], body: body))
        guard (200...299).contains(response.status) else { throw IdenqaError.transport(status: response.status) }
    }

    public func observe(url: URL, ticket: String) -> AsyncThrowingStream<RealtimeEvent, Error> {
        realtime.events(url: url, ticket: ticket)
    }

    // MARK: - Journey operations (owned by the capture journey boundary)

    func bootstrapDetail(capabilities: CapabilityAdvertisement, identity: NativeBootstrapIdentity, now: Date) async throws -> CaptureSessionDocument {
        let response = try await bootstrapResponse(capabilities: capabilities, identity: identity, now: now)
        return try decode(CaptureSessionDocument.self, from: response.body)
    }

    func getSession() async throws -> CaptureSessionDocument {
        let response = try await captureRequest(method: "GET", path: "v1/capture/session")
        return try decode(CaptureSessionDocument.self, from: response.body, status: response.status)
    }

    func getProgress(ifNoneMatch: String? = nil) async throws -> CaptureProgressResult {
        var headers: [String: String] = [:]
        if let ifNoneMatch { headers["If-None-Match"] = ifNoneMatch }
        let response = try await captureRequest(method: "GET", path: "v1/capture/progress", headers: headers)
        if response.status == 304 {
            return CaptureProgressResult(progress: nil, etag: response.header("etag"), notModified: true)
        }
        let document = try decode(CaptureProgressDocument.self, from: response.body, status: response.status)
        return CaptureProgressResult(progress: document, etag: response.header("etag"), notModified: false)
    }

    func cancel(expectedVersion: Int64, idempotencyKey: String) async throws -> CaptureCancellationDocument {
        guard expectedVersion >= 1 else { throw IdenqaError.invalidConfiguration }
        let body = try JSONEncoder.idenqa.encode(CaptureCancelCommand(expectedVersion: expectedVersion))
        let response = try await captureRequest(
            method: "POST",
            path: "v1/capture/cancel",
            headers: ["Idempotency-Key": try structuredString(idempotencyKey)],
            body: body
        )
        return try decode(CaptureCancellationDocument.self, from: response.body, status: response.status)
    }

    func createEvidenceUpload(_ input: CaptureEvidenceUploadCreate, idempotencyKey: String) async throws -> EvidenceUploadResult {
        let body = try JSONEncoder.idenqa.encode(input)
        let response = try await captureRequest(
            method: "POST",
            path: "v1/evidence-uploads",
            headers: ["Idempotency-Key": try structuredString(idempotencyKey)],
            body: body
        )
        let document = try decode(EvidenceUploadDocument.self, from: response.body, status: response.status)
        return EvidenceUploadResult(upload: document, etag: response.header("etag"))
    }

    func uploadEvidence(uploadID: String, contentType: String, body: Data, etag: String, digestHex: String) async throws -> EvidenceUploadResult {
        guard !uploadID.isEmpty, body.count > 0, digestHex.count == 64, digestHex.allSatisfy(\.isHexDigit) else {
            throw IdenqaError.invalidConfiguration
        }
        let headers: [String: String] = [
            "Content-Type": contentType,
            "Content-Length": String(body.count),
            "Content-Digest": CaptureDigest.contentDigestHeader(hex: digestHex),
            "If-Match": etag,
        ]
        let response = try await captureRequest(method: "PUT", path: "v1/evidence-uploads/\(uploadID)", headers: headers, body: body)
        let document = try decode(EvidenceUploadDocument.self, from: response.body, status: response.status)
        return EvidenceUploadResult(upload: document, etag: response.header("etag"))
    }

    // MARK: - Helpers

    private func bootstrapResponse(capabilities: CapabilityAdvertisement, identity: NativeBootstrapIdentity, now: Date) async throws -> TransportResponse {
        guard let token = try await tokenStore.read(), !token.isEmpty else { throw IdenqaError.invalidConfiguration }
        let request = try await identity.request(token: token, capabilities: capabilities, createdAt: now)
        let body = try JSONEncoder.idenqa.encode(request)
        let response = try await transport.send(TransportRequest(
            method: "POST",
            url: baseURL.appending(path: "v1/capture/native/bootstrap"),
            headers: ["Authorization": "Bearer \(token)", "Content-Type": "application/json"],
            body: body
        ))
        guard response.status == 200 else { throw mapStatus(response.status) }
        return response
    }

    private func captureRequest(method: String, path: String, headers: [String: String] = [:], body: Data? = nil) async throws -> TransportResponse {
        guard let token = try await tokenStore.read(), !token.isEmpty else { throw IdenqaError.invalidConfiguration }
        var values = headers
        values["Authorization"] = "Bearer \(token)"
        return try await transport.send(TransportRequest(method: method, url: baseURL.appending(path: path), headers: values, body: body))
    }

    private func decode<T: Decodable>(_ type: T.Type, from body: Data, status: Int = 200) throws -> T {
        guard (200...299).contains(status) else { throw mapStatus(status) }
        do {
            return try JSONDecoder.idenqa.decode(type, from: body)
        } catch {
            throw IdenqaError.invalidResponse
        }
    }

    private func mapStatus(_ status: Int) -> IdenqaError {
        switch status {
        case 401, 403: return .unauthenticated
        case 404: return .notFound
        case 409, 412, 428: return .stateConflict
        case 408, 429, 500, 502, 503, 504: return .transport(status: status)
        case 400, 413, 422: return .invalidResponse
        default: return .transport(status: status)
        }
    }
}

struct CaptureProgressResult: Sendable {
    let progress: CaptureProgressDocument?
    let etag: String?
    let notModified: Bool
}

struct EvidenceUploadResult: Sendable {
    let upload: EvidenceUploadDocument
    let etag: String?
}

enum CaptureDigest {
    static func sha256Hex(_ data: Data) -> String {
        SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    static func contentDigestHeader(hex: String) -> String {
        var bytes: [UInt8] = []
        bytes.reserveCapacity(hex.count / 2)
        var index = hex.startIndex
        while index < hex.endIndex {
            let next = hex.index(index, offsetBy: 2)
            if let value = UInt8(hex[index..<next], radix: 16) { bytes.append(value) }
            index = next
        }
        return "sha-256=:\(Data(bytes).base64EncodedString()):"
    }
}

/// RFC 9651 String encoding for the Idempotency-Key header field value.
func structuredString(_ value: String) throws -> String {
    guard !value.isEmpty else { throw IdenqaError.invalidConfiguration }
    for scalar in value.unicodeScalars {
        guard scalar.isASCII, (0x20...0x7e).contains(scalar.value) else { throw IdenqaError.invalidConfiguration }
    }
    let escaped = value
        .replacingOccurrences(of: "\\", with: "\\\\")
        .replacingOccurrences(of: "\"", with: "\\\"")
    let encoded = "\"\(escaped)\""
    guard encoded.utf8.count <= 130 else { throw IdenqaError.invalidConfiguration }
    return encoded
}

extension JSONEncoder {
    static var idenqa: JSONEncoder {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        encoder.dateEncodingStrategy = .iso8601
        return encoder
    }
}
