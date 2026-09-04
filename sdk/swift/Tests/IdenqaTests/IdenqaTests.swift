import Foundation
import CryptoKit
import Testing
@testable import Idenqa

actor TokenStore: CaptureTokenStore {
    var value: String? = "capture-token"
    func read() -> String? { value }
    func write(_ token: String) { value = token }
    func clear() { value = nil }
}

actor RecordingTransport: HTTPTransport {
    var requests: [TransportRequest] = []
    func send(_ request: TransportRequest) async throws -> TransportResponse {
        requests.append(request)
        return TransportResponse(status: 200, body: Data(#"{"id":"ver_1","state":"collecting","version":1,"region":"ng-1","expires_at":"2026-09-05T12:00:00Z"}"#.utf8))
    }
}

struct EmptyRealtime: RealtimeTransport {
    func events(url: URL, ticket: String) -> AsyncThrowingStream<RealtimeEvent, Error> { AsyncThrowingStream { $0.finish() } }
}

struct ProofKey: NativeProofKey {
    func publicKey() async throws -> Data { Data(repeating: 7, count: 65) }
    func sign(_ message: Data) async throws -> Data { Data(SHA256.hash(data: message)) }
}

@Test func bootstrapAdvertisesCapabilitiesWithoutClaimingAssurance() async throws {
    let transport = RecordingTransport()
    let client = try IdenqaClient(baseURL: #require(URL(string: "https://core.example/")), tokenStore: TokenStore(), transport: transport, realtime: EmptyRealtime())
    let identity = try NativeBootstrapIdentity(applicationID: "dev.idenqa.fixture", key: ProofKey())
    let result = try await client.bootstrap(capabilities: CapabilityAdvertisement(sdkVersion: "0.1.0", implementedMethods: ["camera"], currentlyAvailableMethods: []), identity: identity, now: Date(timeIntervalSince1970: 1_788_523_200))
    #expect(result.region == "ng-1")
    let requests = await transport.requests
    #expect(requests.count == 1)
    #expect(requests[0].headers["Authorization"] == "Bearer capture-token")
    #expect(requests[0].url.path == "/v1/capture/native/bootstrap")
}

@Test func sessionStateRejectsSequenceGaps() async throws {
    let state = CaptureSessionState(session: CaptureSession(id: "ver_1", state: "collecting", version: 1, region: "ng-1", expiresAt: .distantFuture))
    await #expect(throws: IdenqaError.stateConflict) { try await state.apply(RealtimeEvent(sequence: 2, type: "captured", sessionVersion: 2)) }
}

@Test func sharedPublishedFixturesDecode() throws {
    let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().appending(path: "../../../../contracts/capture/native/v1/fixtures").standardizedFileURL
    let session = try JSONDecoder.idenqa.decode(CaptureSession.self, from: Data(contentsOf: root.appending(path: "bootstrap-response.json")))
    let event = try JSONDecoder.idenqa.decode(RealtimeEvent.self, from: Data(contentsOf: root.appending(path: "realtime-event.json")))
    #expect(session.id.hasPrefix("ver_"))
    #expect(event.sequence == 1)
}
