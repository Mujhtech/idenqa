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
        guard let token = try await tokenStore.read(), !token.isEmpty else { throw IdenqaError.invalidConfiguration }
        let request = try await identity.request(token: token, capabilities: capabilities, createdAt: now)
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        let body = try encoder.encode(request)
        let response = try await transport.send(TransportRequest(method: "POST", url: baseURL.appending(path: "v1/capture/native/bootstrap"), headers: ["Authorization": "Bearer \(token)", "Content-Type": "application/json"], body: body))
        guard response.status == 200 else { throw IdenqaError.transport(status: response.status) }
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
}
