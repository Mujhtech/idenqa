import Foundation
import Testing
@testable import Idenqa

private func face(yaw: Double = 0, closed: Double = 0, count: Int = 1) -> CaptureFacePose {
    CaptureFacePose(faceCount: count, yaw: yaw, pitch: 0, roll: 0, centerX: 0.5, centerY: 0.5,
        width: 0.4, height: 0.5, leftEyeClosed: closed, rightEyeClosed: closed)
}

@Test func poseRequiresDirectionAndContiguousMeasuredHold() {
    var gate = CapturePoseGate(challenge: LivenessChallenge(id: "right", prompt: .turnRight, maximumDuration: .seconds(5)))
    for time in stride(from: 0, through: 450, by: 150) { #expect(!gate.update(face(), at: Double(time)).complete) }
    for time in stride(from: 600, through: 1200, by: 150) { #expect(!gate.update(face(yaw: -20), at: Double(time)).complete) }
    #expect(!gate.update(face(yaw: 20), at: 1350).complete)
    #expect(!gate.update(face(yaw: 20), at: 1500).complete)
    #expect(!gate.update(face(yaw: 20), at: 1650, quality: false).complete)
    for time in [1800.0,1950,2100] { #expect(!gate.update(face(yaw: 20), at: time).complete) }
    #expect(gate.update(face(yaw: 20), at: 2250).complete)
}

@Test func poseBlinkRequiresTwoClosedSamplesThenOpenHold() {
    var gate = CapturePoseGate(challenge: LivenessChallenge(id: "blink", prompt: .blink, maximumDuration: .seconds(5)))
    for time in [0.0,150,300,450] { _ = gate.update(face(), at: time) }
    #expect(!gate.update(face(closed: 1), at: 600).complete)
    #expect(!gate.update(face(), at: 750).complete)
    #expect(!gate.update(face(closed: 1), at: 900).complete)
    #expect(!gate.update(face(closed: 1), at: 1000).complete)
    for time in [1100.0,1250,1400] { #expect(!gate.update(face(), at: time).complete) }
    #expect(gate.update(face(), at: 1550).complete)
}

@Test func staleInvalidAndLostFaceMeasurementsNeverComplete() {
    var gate = CapturePoseGate(challenge: LivenessChallenge(id: "neutral", prompt: .neutral, maximumDuration: .seconds(5)))
    _ = gate.update(face(), at: 0)
    _ = gate.update(face(), at: 150)
    #expect(!gate.update(face(), at: 2000).complete)
    #expect(!gate.update(face(), at: 2000).complete)
    #expect(!gate.update(face(yaw: .nan), at: 2150).complete)
    #expect(!gate.update(face(count: 0), at: 2300).complete)
    #expect(!gate.update(face(count: 2), at: 2450).complete)
}

@Test func posePolicyMatchesPublishedBounds() throws {
    #expect(throws: IdenqaError.invalidConfiguration) { try CapturePosePolicy(targetDegrees: 14) }
    #expect(throws: IdenqaError.invalidConfiguration) { try CapturePosePolicy(toleranceDegrees: 9) }
    #expect(throws: IdenqaError.invalidConfiguration) { try CapturePosePolicy(holdDurationMilliseconds: 299) }
    #expect(throws: IdenqaError.invalidConfiguration) { try CapturePosePolicy(targetDegrees: 20.5) }
    #expect(try CapturePosePolicy().holdDurationMilliseconds == 450)
}

@Test func invalidQualityMeasurementsFailClosed() {
    let quality = CaptureQualityMeasurement(width: 720,height: 720,byteCount: 1,brightness: .nan,contrast: 0.5,sharpness: 0.5,glare: 0,faceCount: 1)
    #expect(quality.failures(against: CaptureQualityPolicy(minimumWidth: 320,minimumHeight: 320,maximumBytes: 1024)) == ["invalid_measurement"])
}
