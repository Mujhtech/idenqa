import Foundation

public struct CaptureEvidenceSequence: Codable, Equatable, Sendable {
    public let sequenceDigest: String
    public let index: Int
    public let count: Int
    public let challengeID: String
    public let capturedAt: Date
    public let previousDigest: String?
    enum CodingKeys: String, CodingKey {
        case index, count
        case sequenceDigest = "sequence_digest", challengeID = "challenge_id", capturedAt = "captured_at", previousDigest = "previous_digest"
    }
}

extension CaptureJourney {
    /// Submits the complete measured sequence in order; Core alone confirms the task.
    public func submitSequence(taskID: String, requirement: CaptureRequirement, frames: [AcquiredFrame]) async throws -> CaptureJourneySnapshot {
        let snapshot = currentSnapshot()
        guard let task = snapshot.currentTask, task.id == taskID,
              task.evidenceType == requirement.evidenceType, task.artefact == requirement.artefact,
              (2...8).contains(frames.count), frames.count == requirement.challenges.count,
              frames.map(\.challengeID) == requirement.challenges.map({ Optional($0.id) }) else { throw IdenqaError.invalidConfiguration }
        let digests = frames.map { "sha256:" + CaptureDigest.sha256Hex($0.artifact.bytes) }
        let manifest = try JSONSerialization.data(withJSONObject: ["session_id": snapshot.verificationID ?? "", "requirement_id": requirement.id,
            "challenges": requirement.challenges.map(\.id), "digests": digests], options: [.sortedKeys])
        let sequenceDigest = "sha256:" + CaptureDigest.sha256Hex(manifest)
        for (index, frame) in frames.enumerated() {
            try Task.checkCancellation()
            guard frame.quality.failures(against: requirement.quality).isEmpty else { throw IdenqaError.captureQuality }
            _ = try await submit(taskID: taskID, artifact: frame.artifact, method: "idenqa.method.live_camera",
                sequence: CaptureEvidenceSequence(sequenceDigest: sequenceDigest, index: index, count: frames.count,
                    challengeID: requirement.challenges[index].id, capturedAt: frame.capturedAt,
                    previousDigest: index == 0 ? nil : digests[index - 1]))
        }
        return try await refresh()
    }
}
