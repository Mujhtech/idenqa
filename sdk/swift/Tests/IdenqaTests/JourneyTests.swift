import CryptoKit
import Foundation
import Testing
@testable import Idenqa

// MARK: - Fakes

actor ScriptedTransport: HTTPTransport {
    typealias Handler = @Sendable (TransportRequest) async throws -> TransportResponse
    private var handler: Handler
    private(set) var requests: [TransportRequest] = []

    init(handler: @escaping Handler) { self.handler = handler }

    func setHandler(_ handler: @escaping Handler) { self.handler = handler }

    func send(_ request: TransportRequest) async throws -> TransportResponse {
        requests.append(request)
        return try await handler(request)
    }
}

actor MemoryTokenStore: CaptureTokenStore {
    private var token: String?
    init(token: String? = nil) { self.token = token }
    func read() throws -> String? { token }
    func write(_ value: String) throws { token = value }
    func clear() throws { token = nil }
}

actor MemoryJourneyStore: CaptureJourneyStore {
    private var value: CaptureJourneyReference?
    func read() throws -> CaptureJourneyReference? { value }
    func write(_ reference: CaptureJourneyReference) throws { value = reference }
    func clear() throws { value = nil }
}

actor MemoryTemporaryFiles: CaptureTemporaryFileStore {
    private(set) var files: [String: Data] = [:]
    private(set) var removedReferences: [String] = []
    private(set) var stageCount = 0
    private var maximumFileBytes = 8 * 1024 * 1024

    func setMaximumFileBytes(_ value: Int) { maximumFileBytes = value }

    func stage(_ artifact: CapturedArtifact, category: String, now: Date) async throws -> CaptureTemporaryFile {
        guard artifact.contentType == "image/jpeg", !artifact.bytes.isEmpty,
              artifact.bytes.count <= maximumFileBytes else { throw IdenqaError.temporaryStorage }
        stageCount += 1
        let reference = "\(category)-\(stageCount).bin"
        files[reference] = artifact.bytes
        return CaptureTemporaryFile(reference: reference, byteCount: artifact.bytes.count, createdAt: now)
    }

    func data(for file: CaptureTemporaryFile) async throws -> Data {
        guard let value = files[file.reference] else { throw IdenqaError.invalidConfiguration }
        return value
    }

    func remove(_ file: CaptureTemporaryFile) async throws {
        files.removeValue(forKey: file.reference)
        removedReferences.append(file.reference)
    }

    func purge(olderThan date: Date) async throws -> Int {
        let old = files.filter { _ in true }.count
        removedReferences.append(contentsOf: files.keys)
        files.removeAll()
        return old
    }

    func purgeAll() async throws -> Int {
        let count = files.count
        removedReferences.append(contentsOf: files.keys)
        files.removeAll()
        return count
    }

    func count() async throws -> Int { files.count }
    func totalBytes() async throws -> Int { files.values.reduce(0) { $0 + $1.count } }
}

struct FixtureProofKey: NativeProofKey {
    func publicKey() async throws -> Data { Data(repeating: 7, count: 65) }
    func sign(_ message: Data) async throws -> Data { Data(SHA256.hash(data: message)) }
}

actor TrackingProofKey: NativeProofKey {
    private var cleared = false
    func publicKey() async throws -> Data { Data(repeating: 7, count: 65) }
    func sign(_ message: Data) async throws -> Data { Data(SHA256.hash(data: message)) }
    func clear() { cleared = true }
    func isCleared() async -> Bool { cleared }
}

actor RecordingScreenProtection: CaptureScreenProtection {
    nonisolated let isAvailable: Bool
    private(set) var protected = false
    private var captured = false

    init(available: Bool = true) { self.isAvailable = available }

    func applyProtection() { protected = true }
    func removeProtection() { protected = false }
    func isCaptured() -> Bool { captured }
    func setCaptured(_ value: Bool) { captured = value }
}

struct EmptyRealtime2: RealtimeTransport {
    func events(url: URL, ticket: String) -> AsyncThrowingStream<RealtimeEvent, Error> {
        AsyncThrowingStream { $0.finish() }
    }
}

final class TestTimeSource: CaptureTimeSource, @unchecked Sendable {
    private let lock = NSLock()
    private var value: Date
    init(_ value: Date = Date(timeIntervalSince1970: 1_788_523_200)) { self.value = value }
    func now() -> Date { lock.lock(); defer { lock.unlock() }; return value }
}

final class KeySequence: @unchecked Sendable {
    private let lock = NSLock()
    private var counter = 0
    func make(_ prefix: String) -> String {
        lock.lock()
        defer { lock.unlock() }
        counter += 1
        return "\(prefix)_fixture000\(counter)"
    }
}

struct FixtureWorld {
    let journey: CaptureJourney
    let tokenStore: MemoryTokenStore
    let referenceStore: MemoryJourneyStore
    let temporaryFiles: MemoryTemporaryFiles
    let proofKey: TrackingProofKey
    let transport: ScriptedTransport
    let core: FakeCore
}

func makeWorld(
    core: FakeCore,
    declaredMethods: [String] = ["idenqa.method.live_camera", "idenqa.method.file_upload"],
    availableMethods: [String] = ["idenqa.method.live_camera", "idenqa.method.file_upload"],
    preferredMethods: [String] = [],
    pinnedProfile: String? = nil,
    pinnedRegion: String? = nil,
    temporaryFiles: (any CaptureTemporaryFileStore)? = nil,
    screenProtection: any CaptureScreenProtection = NoopCaptureScreenProtection(),
    keys: KeySequence = KeySequence()
) async throws -> FixtureWorld {
    let tokenStore = MemoryTokenStore()
    let referenceStore = MemoryJourneyStore()
    let temporary = MemoryTemporaryFiles()
    let proofKey = TrackingProofKey()
    let transport = ScriptedTransport { request in try await core.handle(request) }
    let configuration = try CaptureConfiguration(
        coreURL: URL(string: "https://core.example")!,
        applicationID: "dev.idenqa.fixture",
        installationID: "install-1",
        locale: "en-NG",
        region: pinnedRegion,
        captureProfileID: pinnedProfile,
        preferredMethods: preferredMethods
    )
    let capabilities = try CaptureCapabilities(
        platform: "ios",
        sdkVersion: "0.1.0",
        declaredMethods: declaredMethods,
        availableMethods: availableMethods,
        cameraCapture: availableMethods.contains("idenqa.method.live_camera"),
        fileUpload: availableMethods.contains("idenqa.method.file_upload"),
        livenessChallenge: true
    )
    let journey = try CaptureJourney(
        configuration: configuration,
        capabilities: capabilities,
        tokenStore: tokenStore,
        referenceStore: referenceStore,
        proofKey: proofKey,
        transport: transport,
        realtime: EmptyRealtime2(),
        temporaryFiles: temporaryFiles ?? temporary,
        screenProtection: screenProtection,
        time: TestTimeSource(),
        idempotencyKeyFactory: { prefix in keys.make(prefix) }
    )
    return FixtureWorld(
        journey: journey,
        tokenStore: tokenStore,
        referenceStore: referenceStore,
        temporaryFiles: temporary,
        proofKey: proofKey,
        transport: transport,
        core: core
    )
}

// MARK: - Fake Core

actor FakeCore {
    var state = "collecting"
    var version: Int64 = 1
    var profileID = "prf_01J00000000000000000000000"
    var region = "ng-1"
    var requirements: [String: Any]
    var completions: [[String: Any]] = []
    var uploadState = "issued"
    var uploadEtag = "\"1\""
    var cancelMutations = 0
    var cancelKeys: [String] = []
    var issueKeys: [String] = []
    var uploadBodies: [Data] = []
    var uploadHeaders: [[String: String]] = []
    var progressReads = 0
    var nextProgressNetworkFailure = false
    var nextCancelNetworkFailure = false
    var sessionReads = 0
    var uploadStatusOverride: Int?
    var uploadNetworkFailure = false

    init(requirements: [String: Any] = FakeCore.defaultRequirements()) {
        self.requirements = requirements
    }

    static func defaultRequirements() -> [String: Any] {
        [
            "schema_version": 1,
            "registry": ["schema_version": 1, "revision": 1, "digest": "sha256:\(String(repeating: "b", count: 64))"],
            "requirements": [
                requirement(key: "selfie", evidenceType: "idenqa.evidence.selfie_image", artefact: "idenqa.artefact.selfie_image", methods: ["idenqa.method.live_camera"]),
                requirement(key: "document", evidenceType: "idenqa.evidence.document_image", artefact: "idenqa.artefact.document_front", methods: ["idenqa.method.live_camera"]),
            ],
        ]
    }

    static func requirement(
        key: String,
        evidenceType: String,
        artefact: String,
        strategy: String = "any_of",
        methods: [String],
        fallbacks: [[String: Any]] = []
    ) -> [String: Any] {
        [
            "key": key,
            "purpose": "idenqa.purpose.identity_verification",
            "evidence_type": evidenceType,
            "artefacts": [artefact],
            "acquisition": ["strategy": strategy, "methods": methods],
            "required_assurances": [],
            "fallbacks": fallbacks,
        ]
    }

    func sessionJSON() -> Data {
        let document: [String: Any] = [
            "id": "ver_01J00000000000000000000000",
            "state": state,
            "version": version,
            "profile_id": profileID,
            "profile_revision": 1,
            "profile_digest": "sha256:\(String(repeating: "a", count: 64))",
            "policy_id": "pol_01J00000000000000000000000",
            "region": region,
            "requirements": requirements,
            "created_at": "2026-09-04T12:00:00Z",
            "updated_at": "2026-09-04T12:00:00Z",
            "expires_at": "2026-09-05T12:00:00Z",
        ]
        return try! JSONSerialization.data(withJSONObject: document)
    }

    private func progressJSON() -> Data {
        let document: [String: Any] = [
            "verification_id": "ver_01J00000000000000000000000",
            "completions": completions,
        ]
        return try! JSONSerialization.data(withJSONObject: document)
    }

    private func progressETag() -> String {
        let encoded = (try? JSONSerialization.data(withJSONObject: completions)) ?? Data()
        return "\"\(encoded.count)-\(completions.count)\""
    }

    private func uploadJSON() -> Data {
        let document: [String: Any] = [
            "id": "upl_01J00000000000000000000000",
            "evidence_id": "evd_01J00000000000000000000000",
            "state": uploadState,
            "version": uploadState == "accepted" ? 3 : 1,
            "attempt": uploadState == "accepted" ? 1 : 0,
            "requirement_key": "selfie",
            "evidence_type": "idenqa.evidence.selfie_image",
            "artefact": "idenqa.artefact.selfie_image",
            "acquisition_method": "idenqa.method.live_camera",
            "assurances": [],
            "allowed_media_types": ["image/jpeg"],
            "maximum_bytes": 16777216,
            "expected_bytes": 3,
            "media_type": "image/jpeg",
            "region": region,
            "created_at": "2026-09-04T12:00:00Z",
            "updated_at": "2026-09-04T12:00:00Z",
            "expires_at": "2026-09-04T12:15:00Z",
        ]
        return try! JSONSerialization.data(withJSONObject: document)
    }

    private func cancellationJSON() -> Data {
        let document: [String: Any] = [
            "event_id": "evt_01J00000000000000000000000",
            "verification_id": "ver_01J00000000000000000000000",
            "state": "cancelled",
            "version": version,
            "occurred_at": "2026-09-04T12:00:00Z",
        ]
        return try! JSONSerialization.data(withJSONObject: document)
    }

    private var cancelReceipts: [String: Data] = [:]

    func handle(_ request: TransportRequest) throws -> TransportResponse {
        let path = request.url.path
        switch (request.method, path) {
        case ("POST", "/v1/capture/native/bootstrap"):
            return TransportResponse(status: 200, headers: ["cache-control": "no-store"], body: sessionJSON())
        case ("GET", "/v1/capture/session"):
            sessionReads += 1
            return TransportResponse(status: 200, body: sessionJSON())
        case ("GET", "/v1/capture/progress"):
            progressReads += 1
            if nextProgressNetworkFailure {
                nextProgressNetworkFailure = false
                throw URLError(.notConnectedToInternet)
            }
            let etag = progressETag()
            if request.headers["If-None-Match"] == etag {
                return TransportResponse(status: 304, headers: ["etag": etag], body: Data())
            }
            return TransportResponse(status: 200, headers: ["etag": etag], body: progressJSON())
        case ("POST", "/v1/capture/cancel"):
            if nextCancelNetworkFailure {
                nextCancelNetworkFailure = false
                throw URLError(.notConnectedToInternet)
            }
            guard let cancelKey = request.headers["Idempotency-Key"] else {
                return TransportResponse(status: 400, body: Data())
            }
            if let receipt = cancelReceipts[cancelKey] {
                return TransportResponse(status: 200, body: receipt)
            }
            let body = try JSONSerialization.jsonObject(with: request.body ?? Data()) as? [String: Any]
            let expected = (body?["expected_version"] as? NSNumber)?.int64Value ?? 0
            guard expected == version else {
                return TransportResponse(status: 409, body: Data(#"{"code":"CONFLICT"}"#.utf8))
            }
            cancelMutations += 1
            cancelKeys.append(cancelKey)
            state = "cancelled"
            version += 1
            let receipt = cancellationJSON()
            cancelReceipts[cancelKey] = receipt
            return TransportResponse(status: 200, body: receipt)
        case ("POST", "/v1/evidence-uploads"):
            guard let key = request.headers["Idempotency-Key"] else {
                return TransportResponse(status: 400, body: Data())
            }
            issueKeys.append(key)
            return TransportResponse(status: 201, headers: ["etag": "\"1\""], body: uploadJSON())
        case ("PUT", "/v1/evidence-uploads/upl_01J00000000000000000000000"):
            if uploadNetworkFailure {
                uploadNetworkFailure = false
                throw URLError(.notConnectedToInternet)
            }
            if let override = uploadStatusOverride {
                return TransportResponse(status: override, body: Data())
            }
            guard let body = request.body else { return TransportResponse(status: 400, body: Data()) }
            uploadBodies.append(body)
            uploadHeaders.append(request.headers)
            uploadState = "accepted"
            uploadEtag = "\"3\""
            completions.append([
                "upload_id": "upl_01J00000000000000000000000",
                "evidence_id": "evd_01J00000000000000000000000",
                "requirement_key": "selfie",
                "evidence_type": "idenqa.evidence.selfie_image",
                "artefact": "idenqa.artefact.selfie_image",
                "acquisition_method": "idenqa.method.live_camera",
            ])
            return TransportResponse(status: 200, headers: ["etag": uploadEtag], body: uploadJSON())
        default:
            return TransportResponse(status: 404, body: Data())
        }
    }
}

// MARK: - Tests

@Suite(.serialized)
struct JourneyTests {
    @Test func configurationValidatesBoundsAndRejectsSecrets() throws {
        #expect(throws: IdenqaError.invalidConfiguration) {
            try CaptureConfiguration(
                coreURL: URL(string: "http://core.example")!,
                applicationID: "dev.idenqa.fixture",
                installationID: "install-1",
                locale: "en-NG"
            )
        }
        #expect(throws: IdenqaError.invalidConfiguration) {
            try CaptureConfiguration(
                coreURL: URL(string: "https://core.example")!,
                applicationID: "dev.idenqa.fixture",
                installationID: "install-1",
                locale: "not a locale"
            )
        }
        #expect(throws: IdenqaError.invalidConfiguration) {
            try CaptureConfiguration(
                coreURL: URL(string: "https://core.example")!,
                applicationID: "dev.idenqa.fixture",
                installationID: "install-1",
                locale: "en",
                preferredMethods: ["camera"]
            )
        }
        let configuration = try CaptureConfiguration(
            coreURL: URL(string: "https://core.example")!,
            applicationID: "dev.idenqa.fixture",
            installationID: "install-1",
            locale: "en-NG",
            region: "ng-1"
        )
        #expect(configuration.region == "ng-1")
    }

    @Test func startBootstrapsAndRendersOneTaskPerScreen() async throws {
        let core = FakeCore()
        let world = try await makeWorld(core: core)
        let snapshot = try await world.journey.start(bootstrapToken: "idq_cap_v1_fixture")
        #expect(snapshot.status == .captureRequired)
        #expect(snapshot.currentTask?.requirementKey == "selfie")
        #expect(snapshot.remainingTaskCount == 2)
        #expect(snapshot.completedTaskCount == 0)
        #expect(snapshot.canCancel)
        #expect(try await world.tokenStore.read() == "idq_cap_v1_fixture")
        #expect(try await world.referenceStore.read() != nil)
    }

    @Test func resumeAfterProcessDeathReattachesWithoutBootstrap() async throws {
        let core = FakeCore()
        let world = try await makeWorld(core: core)
        _ = try await world.journey.start(bootstrapToken: "idq_cap_v1_fixture")
        let transportRequests = await world.transport.requests
        #expect(transportRequests.contains { $0.url.path == "/v1/capture/native/bootstrap" })

        let restarted = try await makeWorld(core: core)
        try await restarted.tokenStore.write("idq_cap_v1_fixture")
        let stored = try #require(try await world.referenceStore.read())
        try await restarted.referenceStore.write(stored)

        let snapshot = try await restarted.journey.resume()
        #expect(snapshot.status == .captureRequired)
        #expect(snapshot.currentTask?.requirementKey == "selfie")
        let requests = await restarted.transport.requests
        #expect(!requests.contains { $0.url.path == "/v1/capture/native/bootstrap" })
        #expect(requests.contains { $0.url.path == "/v1/capture/session" })
    }

    @Test func resumeWithoutPersistedReferenceFailsClosed() async throws {
        let core = FakeCore()
        let world = try await makeWorld(core: core)
        await #expect(throws: IdenqaError.notStarted) {
            try await world.journey.resume()
        }
    }

    @Test func resumeRestoresAuthoritativeAcceptedProgress() async throws {
        let core = FakeCore()
        await core.setCompletions([[
            "upload_id": "upl_01J00000000000000000000000",
            "evidence_id": "evd_01J00000000000000000000000",
            "requirement_key": "selfie",
            "evidence_type": "idenqa.evidence.selfie_image",
            "artefact": "idenqa.artefact.selfie_image",
            "acquisition_method": "idenqa.method.live_camera",
        ]])
        let world = try await makeWorld(core: core)
        _ = try await world.journey.start(bootstrapToken: "idq_cap_v1_fixture")
        let snapshot = try await world.journey.resume()
        #expect(snapshot.completedTaskCount == 1)
        #expect(snapshot.currentTask?.requirementKey == "document")
    }

    @Test func submitUploadsWithBoundDigestAndCleansTemporaryFile() async throws {
        let core = FakeCore()
        let world = try await makeWorld(core: core)
        let started = try await world.journey.start(bootstrapToken: "idq_cap_v1_fixture")
        let task = try #require(started.currentTask)
        let artifact = CapturedArtifact(bytes: Data([1, 2, 3]), contentType: "image/jpeg", acquisitionMethod: "idenqa.method.live_camera")
        let snapshot = try await world.journey.submit(taskID: task.id, artifact: artifact, method: "idenqa.method.live_camera")
        #expect(snapshot.currentTask?.requirementKey == "document")
        #expect(snapshot.completedTaskCount == 1)
        #expect(try await world.temporaryFiles.count() == 0)
        let headers = try #require(await core.uploadHeaders.first)
        #expect(headers["If-Match"] == "\"1\"")
        #expect(headers["Content-Digest"]?.hasPrefix("sha-256=:") == true)
        let issueKeys = await core.issueKeys
        #expect(issueKeys.first?.hasPrefix("\"capture_upload_") == true)

        await #expect(throws: IdenqaError.stateConflict) {
            try await world.journey.submit(taskID: task.id, artifact: artifact, method: "idenqa.method.live_camera")
        }
    }

    @Test func submitIsBoundedAndCleansTemporaryFileOnFailure() async throws {
        let core = FakeCore()
        await core.setUploadStatus(409)
        let world = try await makeWorld(core: core)
        let started = try await world.journey.start(bootstrapToken: "idq_cap_v1_fixture")
        let task = try #require(started.currentTask)
        let artifact = CapturedArtifact(bytes: Data([1, 2, 3]), contentType: "image/jpeg", acquisitionMethod: "idenqa.method.live_camera")
        await #expect(throws: IdenqaError.stateConflict) {
            try await world.journey.submit(taskID: task.id, artifact: artifact, method: "idenqa.method.live_camera")
        }
        #expect(try await world.temporaryFiles.count() == 0)
        #expect(await world.temporaryFiles.removedReferences.count == 1)
    }

    @Test func submitReportsNetworkFailureAndKeepsProgress() async throws {
        let core = FakeCore()
        await core.setUploadNetworkFailure(true)
        let world = try await makeWorld(core: core)
        let started = try await world.journey.start(bootstrapToken: "idq_cap_v1_fixture")
        let task = try #require(started.currentTask)
        let artifact = CapturedArtifact(bytes: Data([1, 2, 3]), contentType: "image/jpeg", acquisitionMethod: "idenqa.method.live_camera")
        await #expect(throws: IdenqaError.networkUnavailable) {
            try await world.journey.submit(taskID: task.id, artifact: artifact, method: "idenqa.method.live_camera")
        }
        let snapshot = await world.journey.currentSnapshot()
        #expect(snapshot.guidance.code == .networkUnavailable)
        #expect(snapshot.canCancel)
        #expect(try await world.temporaryFiles.count() == 0)
    }

    @Test func cancelRetriesWithTheSamePersistedIdempotencyKey() async throws {
        let core = FakeCore()
        let world = try await makeWorld(core: core)
        _ = try await world.journey.start(bootstrapToken: "idq_cap_v1_fixture")
        await core.setNextCancelNetworkFailure()
        await #expect(throws: IdenqaError.networkUnavailable) {
            try await world.journey.cancel()
        }
        let receipt = try await world.journey.cancel()
        #expect(receipt.state == "cancelled")
        let keys = await core.cancelKeys
        #expect(keys.count == 1)
        #expect(keys[0].hasPrefix("\"capture_cancel_"))

        let restarted = try await makeWorld(core: core)
        if let stored = try await world.referenceStore.read() {
            try await restarted.referenceStore.write(stored)
            try await restarted.tokenStore.write("idq_cap_v1_fixture")
            let replay = try await restarted.journey.cancel()
            #expect(replay.eventID == receipt.eventID)
            #expect(await core.cancelMutations == 1)
        }
    }

    @Test func cancelConflictReconcilesTheAuthoritativeTerminalState() async throws {
        let core = FakeCore()
        await core.setVersion(9)
        let world = try await makeWorld(core: core)
        _ = try await world.journey.start(bootstrapToken: "idq_cap_v1_fixture")
        await core.setVersion(11)
        await #expect(throws: IdenqaError.stateConflict) {
            try await world.journey.cancel()
        }
        let snapshot = await world.journey.currentSnapshot()
        #expect(snapshot.sessionVersion == 11)
    }

    @Test func clearLocalDataIsCompleteAndIdempotent() async throws {
        let core = FakeCore()
        let world = try await makeWorld(core: core)
        _ = try await world.journey.start(bootstrapToken: "idq_cap_v1_fixture")
        _ = try await world.temporaryFiles.stage(
            CapturedArtifact(bytes: Data([9, 9]), contentType: "image/jpeg", acquisitionMethod: "idenqa.method.live_camera"),
            category: "evidence",
            now: Date()
        )
        let report = try await world.journey.clearLocalData()
        #expect(report.allCleared)
        #expect(report.temporaryFilesRemoved == 1)
        #expect(try await world.tokenStore.read() == nil)
        #expect(try await world.referenceStore.read() == nil)
        #expect(await world.proofKey.isCleared())
        #expect(try await world.temporaryFiles.count() == 0)

        let second = try await world.journey.clearLocalData()
        #expect(second.allCleared)
        #expect(second.temporaryFilesRemoved == 0)
        let snapshot = await world.journey.currentSnapshot()
        #expect(snapshot.status == .idle)
    }

    @Test func capabilitySelectionFailsClosedAndHonoursApprovedFallbacks() async throws {
        let core = FakeCore(requirements: [
            "schema_version": 1,
            "registry": ["schema_version": 1, "revision": 1, "digest": "sha256:\(String(repeating: "b", count: 64))"],
            "requirements": [
                FakeCore.requirement(
                    key: "selfie",
                    evidenceType: "idenqa.evidence.selfie_image",
                    artefact: "idenqa.artefact.selfie_image",
                    methods: ["idenqa.method.live_camera", "idenqa.method.file_upload"]
                ),
            ],
        ])
        let fileOnly = try await makeWorld(
            core: core,
            declaredMethods: ["idenqa.method.file_upload"],
            availableMethods: ["idenqa.method.file_upload"]
        )
        let capability = await fileOnly.journey.getCapabilities()
        #expect(capability.advertisement().currentlyAvailableMethods == ["idenqa.method.file_upload"])
        #expect(!capability.advertisement().implementedMethods.contains { $0.contains("assurance") })
        let snapshot = try await fileOnly.journey.start(bootstrapToken: "idq_cap_v1_fixture")
        #expect(snapshot.currentTask?.methodOptions == ["idenqa.method.file_upload"])
    }

    @Test func unsupportedAllOfFailsClosedWithoutInventingFallbacks() async throws {
        let core = FakeCore(requirements: [
            "schema_version": 1,
            "registry": ["schema_version": 1, "revision": 1, "digest": "sha256:\(String(repeating: "b", count: 64))"],
            "requirements": [
                FakeCore.requirement(
                    key: "selfie",
                    evidenceType: "idenqa.evidence.selfie_image",
                    artefact: "idenqa.artefact.selfie_image",
                    strategy: "all_of",
                    methods: ["idenqa.method.live_camera", "idenqa.method.file_upload"]
                ),
            ],
        ])
        let world = try await makeWorld(
            core: core,
            declaredMethods: ["idenqa.method.file_upload"],
            availableMethods: ["idenqa.method.file_upload"]
        )
        await #expect(throws: IdenqaError.noCompatibleMethod) {
            try await world.journey.start(bootstrapToken: "idq_cap_v1_fixture")
        }
    }

    @Test func policyApprovedMethodFallbackIsSelected() async throws {
        let core = FakeCore(requirements: [
            "schema_version": 1,
            "registry": ["schema_version": 1, "revision": 1, "digest": "sha256:\(String(repeating: "b", count: 64))"],
            "requirements": [
                FakeCore.requirement(
                    key: "selfie",
                    evidenceType: "idenqa.evidence.selfie_image",
                    artefact: "idenqa.artefact.selfie_image",
                    methods: ["vendor.unknown.liveness"],
                    fallbacks: [[
                        "on": ["method_unavailable"],
                        "acquisition": ["strategy": "any_of", "methods": ["idenqa.method.file_upload"]],
                    ]]
                ),
            ],
        ])
        let world = try await makeWorld(core: core)
        let snapshot = try await world.journey.start(bootstrapToken: "idq_cap_v1_fixture")
        #expect(snapshot.currentTask?.fallbackCondition == .methodUnavailable)
        #expect(snapshot.currentTask?.methodOptions == ["idenqa.method.file_upload"])
    }

    @Test func captureFailureAppliesOnlyPolicyApprovedFallback() async throws {
        let core = FakeCore(requirements: [
            "schema_version": 1,
            "registry": ["schema_version": 1, "revision": 1, "digest": "sha256:\(String(repeating: "b", count: 64))"],
            "requirements": [
                FakeCore.requirement(
                    key: "selfie",
                    evidenceType: "idenqa.evidence.selfie_image",
                    artefact: "idenqa.artefact.selfie_image",
                    methods: ["idenqa.method.live_camera"],
                    fallbacks: [[
                        "on": ["capture_failed"],
                        "acquisition": ["strategy": "any_of", "methods": ["idenqa.method.file_upload"]],
                    ]]
                ),
            ],
        ])
        let world = try await makeWorld(core: core)
        let started = try await world.journey.start(bootstrapToken: "idq_cap_v1_fixture")
        let task = try #require(started.currentTask)
        let failed = try await world.journey.captureFailed(taskID: task.id)
        #expect(failed.currentTask?.fallbackCondition == .captureFailed)
        #expect(failed.currentTask?.methodOptions == ["idenqa.method.file_upload"])
        #expect(failed.guidance.code == .qualityRejected)

        let missing = FakeCore(requirements: [
            "schema_version": 1,
            "registry": ["schema_version": 1, "revision": 1, "digest": "sha256:\(String(repeating: "b", count: 64))"],
            "requirements": [
                FakeCore.requirement(
                    key: "selfie",
                    evidenceType: "idenqa.evidence.selfie_image",
                    artefact: "idenqa.artefact.selfie_image",
                    methods: ["idenqa.method.live_camera"]
                ),
            ],
        ])
        let world2 = try await makeWorld(core: missing)
        let started2 = try await world2.journey.start(bootstrapToken: "idq_cap_v1_fixture")
        let task2 = try #require(started2.currentTask)
        await #expect(throws: IdenqaError.noCompatibleMethod) {
            try await world2.journey.captureFailed(taskID: task2.id)
        }
    }

    @Test func lifecycleTransitionsKeepCancellationReachable() async throws {
        let core = FakeCore()
        let world = try await makeWorld(core: core)
        _ = try await world.journey.start(bootstrapToken: "idq_cap_v1_fixture")
        #expect(await world.journey.applicationEnteredBackground().guidance.code == .backgrounded)
        #expect(await world.journey.currentSnapshot().canCancel)
        let foreground = try await world.journey.applicationEnteredForeground()
        #expect(foreground.guidance.code == .none)
        #expect(await world.journey.captureInterruptionBegan().guidance.code == .interrupted)
        #expect(await world.journey.captureInterruptionEnded().guidance.code == .none)
        #expect(await world.journey.networkBecameUnavailable().guidance.code == .networkUnavailable)
        #expect(await world.journey.networkBecameAvailable().guidance.code == .none)
        let denied = await world.journey.cameraPermissionChanged(granted: false)
        #expect(denied.guidance.code == .cameraPermissionDenied)
        #expect(denied.guidance.recovery == .openSettings)
        #expect(await world.journey.cameraPermissionChanged(granted: true).guidance.code == .none)
        #expect(await world.journey.screenCaptureChanged(isCaptured: true).guidance.code == .secureScreenCaptured)
        #expect(await world.journey.screenCaptureChanged(isCaptured: false).guidance.code == .none)
    }

    @Test func backgroundingDoesNotDuplicateWorkOrLoseTheReference() async throws {
        let core = FakeCore()
        let world = try await makeWorld(core: core)
        _ = try await world.journey.start(bootstrapToken: "idq_cap_v1_fixture")
        _ = await world.journey.applicationEnteredBackground()
        _ = try await world.journey.applicationEnteredForeground()
        _ = try await world.journey.applicationEnteredForeground()
        let stored = try #require(try await world.referenceStore.read())
        #expect(stored.verificationID == "ver_01J00000000000000000000000")
        #expect(await core.cancelMutations == 0)
        #expect(await core.uploadBodies.isEmpty)
    }

    @Test func fileBackedTemporaryStoreEnforcesBoundsAndPurges() async throws {
        let directory = FileManager.default.temporaryDirectory.appending(path: "idenqa-tests-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: directory) }
        let store = try FileManagerCaptureTemporaryFileStore(
            namespace: "install-1",
            maximumFileBytes: 1_024,
            maximumTotalBytes: 1_500,
            directory: directory
        )
        let artifact = CapturedArtifact(bytes: Data(repeating: 5, count: 2_048), contentType: "image/jpeg", acquisitionMethod: "idenqa.method.live_camera")
        await #expect(throws: IdenqaError.temporaryStorage) {
            try await store.stage(artifact, category: "evidence", now: Date())
        }
        let small = CapturedArtifact(bytes: Data(repeating: 5, count: 1_024), contentType: "image/jpeg", acquisitionMethod: "idenqa.method.live_camera")
        let staged = try await store.stage(small, category: "evidence", now: Date())
        #expect(try await store.count() == 1)
        #expect(try await store.totalBytes() == 1_024)
        #expect(try await store.data(for: staged) == small.bytes)
        await #expect(throws: IdenqaError.temporaryStorage) {
            try await store.stage(small, category: "evidence", now: Date())
        }
        #expect(try await store.purgeAll() == 1)
        #expect(try await store.count() == 0)
    }

    @Test func sensitiveScreenProtectionIsAppliedAndRemovable() async throws {
        let core = FakeCore()
        let protection = RecordingScreenProtection()
        let world = try await makeWorld(core: core, screenProtection: protection)
        _ = try await world.journey.start(bootstrapToken: "idq_cap_v1_fixture")
        let applied = await world.journey.applySensitiveScreenProtection()
        #expect(applied)
        #expect(await protection.protected)
        await protection.setCaptured(true)
        #expect(await world.journey.screenCaptureState())
        await world.journey.removeSensitiveScreenProtection()
        #expect(await protection.protected == false)
    }

    @Test func persistedReferenceRoundTripsThroughTheSecureCodec() throws {
        let reference = CaptureJourneyReference(
            verificationID: "ver_1",
            sessionVersion: 3,
            region: "ng-1",
            profileRevision: 1,
            profileDigest: "sha256:\(String(repeating: "a", count: 64))",
            expiresAt: Date(timeIntervalSince1970: 1_788_523_200),
            completedTaskIDs: ["a", "b"],
            failedTaskIDs: ["c"],
            idempotencyKeys: ["cancel": "capture_cancel_1", "upload.issue:x": "capture_upload_2"]
        )
        let encoded = try CaptureJourneyReferenceCodec.encode(reference)
        #expect(try CaptureJourneyReferenceCodec.decode(encoded) == reference)
        #expect(!encoded.contains("bytes"))
        #expect(!encoded.contains("evidence"))
    }

    @Test func journeyDecodesTheSharedNativeFixture() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().appending(path: "../../../../contracts/capture/native/v1/fixtures").standardizedFileURL
        let data = try Data(contentsOf: root.appending(path: "bootstrap-response.json"))
        let document = try JSONDecoder.idenqa.decode(CaptureSessionDocument.self, from: data)
        #expect(document.region == "ng-1")
        #expect(document.requirements.requirements.isEmpty)
    }
}

extension FakeCore {
    func setCompletions(_ values: [[String: Any]]) { completions = values }
    func setVersion(_ value: Int64) { version = value }
    func setUploadStatus(_ status: Int) { uploadStatusOverride = status }
    func setUploadNetworkFailure(_ value: Bool) { uploadNetworkFailure = value }
    func setNextCancelNetworkFailure() { nextCancelNetworkFailure = true }
}
