import Foundation
import Testing
@testable import Idenqa

private actor FrameSource: RawCaptureSource {
    private var count = 0
    func capture() -> CapturedArtifact {
        count += 1
        return CapturedArtifact(bytes: Data([UInt8(count)]), contentType: "image/jpeg", acquisitionMethod: "idenqa.method.live_camera")
    }
}

private struct PassingAssessor: CaptureQualityAssessor {
    func assess(_ artifact: CapturedArtifact) async throws -> CaptureQualityMeasurement {
        CaptureQualityMeasurement(width: 1280, height: 720, byteCount: artifact.bytes.count, brightness: 0.5, contrast: 0.4, sharpness: 0.3, glare: 0.01, faceCount: 1)
    }
}

private actor Presenter: ChallengePresenter, CapturePoseTracker {
    private(set) var ids: [String] = []
    private var prompt: LivenessPrompt = .neutral
    private var samples = 0
    func present(_ challenge: LivenessChallenge) { ids.append(challenge.id); prompt = challenge.prompt; samples = 0 }
    func measure(_ artifact: CapturedArtifact) -> CaptureFacePose {
        samples += 1
        let yaw: Double = samples <= 8 ? 0 : prompt == .turnLeft ? -20 : prompt == .turnRight ? 20 : 0
        return CaptureFacePose(faceCount: 1, yaw: yaw, pitch: 0, roll: 0, centerX: 0.5, centerY: 0.5, width: 0.4, height: 0.5, leftEyeClosed: 0, rightEyeClosed: 0)
    }
}

private struct HangingSource: RawCaptureSource {
    func capture() async throws -> CapturedArtifact {
        try await Task.sleep(for: .seconds(60))
        return CapturedArtifact(bytes: Data([1]), contentType: "image/jpeg", acquisitionMethod: "idenqa.method.live_camera")
    }
}

@Test func acquisitionPreservesOrderedChallengeTranscriptWithoutClaimingLiveness() async throws {
    let presenter = Presenter()
    let coordinator = AcquisitionCoordinator(source: FrameSource(), assessor: PassingAssessor(), presenter: presenter, tracker: presenter)
    let policy = CaptureQualityPolicy(minimumWidth: 720, minimumHeight: 720, maximumBytes: 1024, requiredFaceCount: 1)
    let challenges = [
        LivenessChallenge(id: "neutral", prompt: .neutral, maximumDuration: .seconds(5)),
        LivenessChallenge(id: "left", prompt: .turnLeft, maximumDuration: .seconds(5)),
        LivenessChallenge(id: "right", prompt: .turnRight, maximumDuration: .seconds(5)),
    ]
    let frames = try await coordinator.acquire(CaptureRequirement(
        id: "selfie.live",
        evidenceType: "idenqa.evidence.selfie_image",
        artefact: "idenqa.artefact.selfie_image",
        camera: .front,
        quality: policy,
        challenges: challenges
    ))
    #expect(frames.map(\.challengeID) == ["neutral", "left", "right"])
    #expect(await presenter.ids == ["neutral", "left", "right"])
}

@Test func acquisitionRejectsQualityBeforeReturningRawFrames() async throws {
    let coordinator = AcquisitionCoordinator(source: FrameSource(), assessor: PassingAssessor(), presenter: Presenter())
    let policy = CaptureQualityPolicy(minimumWidth: 2000, minimumHeight: 2000, maximumBytes: 1024)
    await #expect(throws: IdenqaError.captureQuality) {
        try await coordinator.acquire(CaptureRequirement(
            id: "document.front",
            evidenceType: "idenqa.evidence.document_image",
            artefact: "idenqa.artefact.document_front",
            camera: .back,
            quality: policy
        ))
    }
}

@Test func acquisitionEnforcesServerChallengeDeadline() async throws {
    let presenter = Presenter()
    let coordinator = AcquisitionCoordinator(source: HangingSource(), assessor: PassingAssessor(), presenter: presenter, tracker: presenter)
    let requirement = CaptureRequirement(
        id: "selfie.live",
        evidenceType: "idenqa.evidence.selfie_image",
        artefact: "idenqa.artefact.selfie_image",
        camera: .front,
        quality: CaptureQualityPolicy(minimumWidth: 720, minimumHeight: 720, maximumBytes: 1024),
        challenges: [LivenessChallenge(id: "neutral", prompt: .neutral, maximumDuration: .milliseconds(500))]
    )
    await #expect(throws: IdenqaError.captureTimeout) {
        try await coordinator.acquire(requirement)
    }
}

@Test func challengedAcquisitionCannotFallBackToTimedPhoto() async throws {
    let presenter = Presenter()
    let coordinator = AcquisitionCoordinator(source: FrameSource(), assessor: PassingAssessor(), presenter: presenter)
    let requirement = CaptureRequirement(id: "selfie.live", evidenceType: "idenqa.evidence.selfie_image",
        artefact: "idenqa.artefact.selfie_image", camera: .front,
        quality: CaptureQualityPolicy(minimumWidth: 720, minimumHeight: 720, maximumBytes: 1024),
        challenges: [LivenessChallenge(id: "left", prompt: .turnLeft, maximumDuration: .seconds(5))])
    await #expect(throws: IdenqaError.noCompatibleMethod) { try await coordinator.acquire(requirement) }
    #expect(await presenter.ids.isEmpty)
}
