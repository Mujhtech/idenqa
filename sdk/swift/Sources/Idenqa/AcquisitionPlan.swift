import Foundation

/// An acquisition plan supplied by the integrating backend. A local capability
/// advertisement is never a substitute for this session-bound policy.
public struct CaptureAcquisitionPlan: Sendable {
    public let sessionID: String
    public let requirements: [CaptureRequirement]

    public init(sessionID: String, requirements: [CaptureRequirement]) throws {
        guard !sessionID.isEmpty, (1...16).contains(requirements.count),
              Set(requirements.map(\.id)).count == requirements.count,
              Set(requirements.map { "\($0.evidenceType):\($0.artefact)" }).count == requirements.count else {
            throw IdenqaError.invalidConfiguration
        }
        for requirement in requirements { try requirement.validate() }
        self.sessionID = sessionID
        self.requirements = requirements
    }

    public func requirement(sessionID: String, task: CaptureTask) throws -> CaptureRequirement {
        guard self.sessionID == sessionID, task.methodOptions.contains("idenqa.method.live_camera") else { throw IdenqaError.stateConflict }
        let matches = requirements.filter { $0.evidenceType == task.evidenceType && $0.artefact == task.artefact }
        guard matches.count == 1 else { throw IdenqaError.invalidConfiguration }
        return matches[0]
    }
}

/// Resolves a backend-issued plan after session attachment and document selection.
public protocol CaptureAcquisitionPlanProvider: Sendable {
    func plan(sessionID: String) async throws -> CaptureAcquisitionPlan
}

extension CaptureRequirement {
    func validate() throws {
        let q = quality
        guard !id.isEmpty, !evidenceType.isEmpty, !artefact.isEmpty,
              (320...16384).contains(q.minimumWidth), (320...16384).contains(q.minimumHeight),
              (1024...20971520).contains(q.maximumBytes), challenges.count <= 8,
              Set(challenges.map(\.id)).count == challenges.count,
              [q.minimumBrightness, q.maximumBrightness, q.minimumContrast, q.minimumSharpness, q.maximumGlare]
                .compactMap({ $0 }).allSatisfy({ $0.isFinite && (0...1).contains($0) }),
              q.requiredFaceCount.map({ (0...4).contains($0) }) ?? true,
              (q.minimumBrightness ?? 0) <= (q.maximumBrightness ?? 1) else { throw IdenqaError.invalidConfiguration }
        for challenge in challenges {
            guard !challenge.id.isEmpty, challenge.maximumDuration >= .milliseconds(500),
                  challenge.maximumDuration <= .seconds(15) else { throw IdenqaError.invalidConfiguration }
            if let pose = challenge.pose, challenge.maximumDuration < .milliseconds(Int64(pose.holdDurationMilliseconds * 2 + 500)) {
                throw IdenqaError.invalidConfiguration
            }
        }
    }
}

extension AcquisitionCoordinator {
    /// Review and submission share this gate; a rejected frame never reaches upload.
    public static func validate(_ artifact: CapturedArtifact, requirement: CaptureRequirement,
                                assessor: any CaptureQualityAssessor) async throws -> CaptureQualityMeasurement {
        try requirement.validate()
        guard artifact.acquisitionMethod == "idenqa.method.live_camera", artifact.contentType == "image/jpeg",
              !artifact.bytes.isEmpty, artifact.bytes.count <= requirement.quality.maximumBytes else { throw IdenqaError.captureQuality }
        let quality = try await assessor.assess(artifact)
        try Task.checkCancellation()
        guard quality.failures(against: requirement.quality).isEmpty else { throw IdenqaError.captureQuality }
        return quality
    }
}
