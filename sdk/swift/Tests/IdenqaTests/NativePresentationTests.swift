import Foundation
import Testing
@testable import Idenqa

actor NoticeFixture {
    var action: String?
    var keys: [String] = []
    func response() throws -> Data {
        var value: [String: Any] = [
            "authority": ["id":"auth", "subject_id":"subject", "verification_id":"ver_01J00000000000000000000000",
                "notice_id":"notice", "consent_required":true, "state":"active", "valid_from":"2026-01-01T00:00:00Z",
                "expires_at":"2027-01-01T00:00:00Z", "regions":["ng-1"], "jurisdiction":"NG",
                "retention_reference":"retention.v1", "requirement_purposes":["idenqa.purpose.identity_verification"],
                "evidence_types":["idenqa.evidence.selfie_image","idenqa.evidence.document_image"]],
            "notice": ["id":"notice", "locale":"en-NG", "controller":"Example tenant", "recipient":"Example processor",
                "copy":["title":"Identity Notice", "summary":"Exact summary <not markup>", "purpose":"Verify identity", "consequences":"You may refuse"]]
        ]
        if let action { value["latest_response"] = receipt(action) }
        return try JSONSerialization.data(withJSONObject: value)
    }
    func respond(_ request: TransportRequest) throws -> TransportResponse {
        let body = try #require(JSONSerialization.jsonObject(with: request.body!) as? [String:String])
        action = body["action"]; keys.append(request.headers["Idempotency-Key"] ?? "")
        return TransportResponse(status: 201, body: try JSONSerialization.data(withJSONObject: receipt(action!)))
    }
    func receipt(_ action: String) -> [String:String] {
        ["authority_id":"auth", "notice_id":"notice", "subject_id":"subject", "verification_id":"ver_01J00000000000000000000000", "action":action, "locale":"en-NG"]
    }
}

func presentationWorld() async throws -> (FixtureWorld, NoticeFixture) {
    let core = FakeCore(); let world = try await makeWorld(core: core); let notice = NoticeFixture()
    await world.transport.setHandler { request in
        if request.url.path == "/v1/capture/authority" { return TransportResponse(status: 200, body: try await notice.response()) }
        if request.url.path == "/v1/capture/authority/responses" { return try await notice.respond(request) }
        return try await core.handle(request)
    }
    return (world, notice)
}

@Test func nativeNoticeRequiresExplicitBoundConsentAndReusesRetryKey() async throws {
    let (world, fixture) = try await presentationWorld()
    _ = try await world.journey.start(bootstrapToken: "fixture")
    let initial = try await world.journey.notice()
    #expect(!initial.accepted)
    #expect(initial.notice.copy.summary == "Exact summary <not markup>")
    await #expect(throws: IdenqaError.invalidConfiguration) { try await world.journey.respondToNotice(.acknowledge) }
    #expect(try await world.journey.respondToNotice(.consent).accepted)
    #expect(try await world.journey.respondToNotice(.consent).accepted)
    let keys = await fixture.keys
    #expect(keys.count == 2 && keys[0] == keys[1] && !keys[0].isEmpty)
    let refused = try await world.journey.respondToNotice(.refuse)
    #expect(refused.refused && !refused.accepted)
}

@Test func nativeNoticeRejectsForeignExpiredAndOutOfScopeAuthority() async throws {
    let core = FakeCore()
    let session = try JSONDecoder.idenqa.decode(CaptureSessionDocument.self, from: await core.sessionJSON())
    let fixture = NoticeFixture()
    let data = try await fixture.response()
    for (key, value) in [("verification_id", "foreign"), ("notice_id", "foreign"), ("state", "withdrawn"), ("expires_at", "2020-01-01T00:00:00Z")] {
        var json = try #require(JSONSerialization.jsonObject(with: data) as? [String:Any])
        var authority = try #require(json["authority"] as? [String:Any]); authority[key] = value; json["authority"] = authority
        let document = try JSONDecoder.idenqa.decode(NativeAuthoritySnapshot.self, from: JSONSerialization.data(withJSONObject: json))
        #expect(throws: IdenqaError.stateConflict) { try document.validated(for: session, now: Date(timeIntervalSince1970: 1_788_523_200)) }
    }
}

@Test func nativePlannerUsesPinnedDocumentBranchAndFrontBeforeBack() async throws {
    let core = FakeCore()
    var session = try JSONDecoder.idenqa.decode(CaptureSessionDocument.self, from: await core.sessionJSON())
    var json = try #require(JSONSerialization.jsonObject(with: JSONEncoder.idenqa.encode(session)) as? [String:Any])
    var requirements = FakeCore.defaultRequirements()
    var requirement = FakeCore.requirement(key: "document", evidenceType: "idenqa.evidence.document_image", artefact: "idenqa.artefact.document_front", methods: ["idenqa.method.live_camera"])
    requirement["artefacts"] = ["idenqa.artefact.document_back","idenqa.artefact.document_front"]
    requirement["document_options"] = [["id":"passport", "label":"passport", "artefacts":["idenqa.artefact.document_front"]],
        ["id":"national_id", "label":"national identity card", "artefacts":["idenqa.artefact.document_back","idenqa.artefact.document_front"]]]
    requirements["requirements"] = [requirement]; json["requirements"] = requirements
    session = try JSONDecoder.idenqa.decode(CaptureSessionDocument.self, from: JSONSerialization.data(withJSONObject: json))
    let capabilities = try CaptureCapabilities(platform: "ios", sdkVersion: "0.1.0", declaredMethods: ["idenqa.method.live_camera"], availableMethods: ["idenqa.method.live_camera"], cameraCapture: true, fileUpload: false, livenessChallenge: false)
    session.documentSelections = ["document":"passport"]
    #expect(try CapturePlanBuilder.build(session: session, capabilities: capabilities, preferences: [], progress: [], captureFailedTaskIDs: []).tasks.count == 1)
    session.documentSelections = ["document":"national_id"]
    let plan = try CapturePlanBuilder.build(session: session, capabilities: capabilities, preferences: [], progress: [], captureFailedTaskIDs: [])
    #expect(plan.tasks.map(\.artefact) == ["idenqa.artefact.document_front","idenqa.artefact.document_back"])
    session.documentSelections = ["document":"unpublished"]
    #expect(throws: IdenqaError.invalidResponse) { try CapturePlanBuilder.build(session: session, capabilities: capabilities, preferences: [], progress: [], captureFailedTaskIDs: []) }
}

#if os(iOS)
import SwiftUI

private struct PresentationPlan: CaptureAcquisitionPlanProvider {
    func plan(sessionID: String) async throws -> CaptureAcquisitionPlan {
        try CaptureAcquisitionPlan(sessionID: sessionID, requirements: [CaptureRequirement(id: "selfie",
            evidenceType: "idenqa.evidence.selfie_image", artefact: "idenqa.artefact.selfie_image", camera: .front,
            quality: CaptureQualityPolicy(minimumWidth: 720, minimumHeight: 720, maximumBytes: 1024))])
    }
}
private struct PresentationAssessor: CaptureQualityAssessor {
    func assess(_ artifact: CapturedArtifact) async throws -> CaptureQualityMeasurement {
        CaptureQualityMeasurement(width: 1280, height: 720, byteCount: artifact.bytes.count,
            brightness: 0.5, contrast: 0.5, sharpness: 0.5, glare: 0, faceCount: 1)
    }
}

@Test @MainActor func nativePresentationDoesNotExposeCameraBeforeConsentAndRefusal() async throws {
    let (world, _) = try await presentationWorld()
    let model = NativeCaptureModel(journey: world.journey, bootstrapToken: "fixture")
    model.load()
    while model.busy { await Task.yield() }
    #expect(model.screen == .notice && !model.cameraVisible)
    model.respond(refuse: true)
    while model.busy { await Task.yield() }
    #expect(model.screen == .refused && !model.cameraVisible)
    #expect(model.snapshot.canCancel)
}

@Test @MainActor func nativePresentationAcceptsConsentAndClearsUnfinishedPhotoOnBackground() async throws {
    let (world, _) = try await presentationWorld()
    let model = NativeCaptureModel(journey: world.journey, bootstrapToken: "fixture",
        acquisitionPlans: PresentationPlan(), qualityAssessor: PresentationAssessor())
    model.load(); while model.busy { await Task.yield() }
    model.respond(refuse: false); while model.busy { await Task.yield() }
    #expect(model.screen == .prepare)
    model.receivedPhoto(CapturedArtifact(bytes: Data([1,2,3]),contentType: "image/jpeg",acquisitionMethod: "idenqa.method.live_camera"))
    while model.busy { await Task.yield() }
    #expect(model.screen == .review && model.photo != nil)
    model.lifecycle(active: false)
    #expect(model.screen == .recovery && model.photo == nil && !model.cameraVisible)
    model.cancel(); while model.busy { await Task.yield() }
    #expect(model.snapshot.status == .cancelled)
}
#endif
