import Foundation

/// Subject-relative degrees (right/up positive), independent of preview mirroring.
/// Measurements describe local pose compliance, not presentation-attack detection.
public struct CaptureFacePose: Sendable {
    public let faceCount: Int
    public let yaw, pitch, roll, centerX, centerY, width, height, leftEyeClosed, rightEyeClosed: Double
    public init(faceCount: Int, yaw: Double, pitch: Double, roll: Double, centerX: Double, centerY: Double,
                width: Double, height: Double, leftEyeClosed: Double, rightEyeClosed: Double) {
        self.faceCount = faceCount; self.yaw = yaw; self.pitch = pitch; self.roll = roll
        self.centerX = centerX; self.centerY = centerY; self.width = width; self.height = height
        self.leftEyeClosed = leftEyeClosed; self.rightEyeClosed = rightEyeClosed
    }
}

public struct CapturePosePolicy: Equatable, Sendable {
    public let targetDegrees, toleranceDegrees, holdDurationMilliseconds: Double
    public init(targetDegrees: Double = 20, toleranceDegrees: Double = 7, holdDurationMilliseconds: Double = 450) throws {
        guard targetDegrees.isFinite, toleranceDegrees.isFinite, holdDurationMilliseconds.isFinite,
              [targetDegrees,toleranceDegrees,holdDurationMilliseconds].allSatisfy({ $0.rounded() == $0 }),
              (15...30).contains(targetDegrees), (3...8).contains(toleranceDegrees),
              (300...1500).contains(holdDurationMilliseconds) else { throw IdenqaError.invalidConfiguration }
        self.targetDegrees = targetDegrees; self.toleranceDegrees = toleranceDegrees
        self.holdDurationMilliseconds = holdDurationMilliseconds
    }
}

public struct CapturePoseProgress: Sendable {
    public let fraction: Double
    public let feedback: String
    public let complete: Bool
}

public protocol CapturePoseTracker: Sendable {
    func measure(_ artifact: CapturedArtifact) async throws -> CaptureFacePose
}

/// One fresh gate per challenge, matching Capture Web's neutral calibration,
/// contiguous holds, stale-sample reset and closed-then-open blink sequence.
public struct CapturePoseGate: Sendable {
    private let prompt: LivenessPrompt
    private let target, tolerance, duration: Double
    private var baseline: CaptureFacePose?
    private var since: Double?
    private var last = -Double.infinity
    private var samples = 0
    private var blinkClosed = false
    private var blinkSamples = 0

    public init(challenge: LivenessChallenge) {
        prompt = challenge.prompt
        target = challenge.pose?.targetDegrees ?? 20
        tolerance = challenge.pose?.toleranceDegrees ?? 7
        duration = challenge.pose?.holdDurationMilliseconds ?? 450
    }

    public mutating func reset() { since = nil; samples = 0; blinkClosed = false; blinkSamples = 0 }

    private mutating func reject(_ feedback: String) -> CapturePoseProgress {
        reset(); return CapturePoseProgress(fraction: 0, feedback: feedback, complete: false)
    }

    public mutating func update(_ pose: CaptureFacePose, at milliseconds: Double, quality: Bool = true) -> CapturePoseProgress {
        guard milliseconds.isFinite, milliseconds > last else { return reject("find_face") }
        if milliseconds - last > 500 { reset() }
        last = milliseconds
        guard [pose.yaw,pose.pitch,pose.roll,pose.centerX,pose.centerY,pose.width,pose.height,pose.leftEyeClosed,pose.rightEyeClosed].allSatisfy(\.isFinite),
              [pose.centerX,pose.centerY,pose.width,pose.height,pose.leftEyeClosed,pose.rightEyeClosed].allSatisfy({ (0...1).contains($0) }), pose.faceCount > 0 else {
            baseline = nil; return reject("find_face")
        }
        guard pose.faceCount == 1 else { baseline = nil; return reject("one_face") }
        guard quality else { return reject("quality") }
        if pose.width < 0.22 || pose.height < 0.28 { return reject("move_closer") }
        if pose.width > 0.8 || pose.height > 0.88 { return reject("move_back") }
        if abs(pose.centerX - 0.5) > 0.18 || abs(pose.centerY - 0.5) > 0.2 || abs(pose.roll) > 12 { return reject("center_face") }
        let neutral = abs(pose.yaw) <= 10 && abs(pose.pitch) <= 12
        guard let baseline else {
            guard neutral, pose.leftEyeClosed <= 0.35, pose.rightEyeClosed <= 0.35 else { return reject("face_forward") }
            let held = hold(milliseconds)
            if prompt == .neutral { return CapturePoseProgress(fraction: held, feedback: "hold_still", complete: held == 1) }
            if held == 1 { self.baseline = pose; reset() }
            return CapturePoseProgress(fraction: 0, feedback: "face_forward", complete: false)
        }
        let yaw = pose.yaw - baseline.yaw, pitch = pose.pitch - baseline.pitch
        if prompt == .blink {
            guard neutral else { return reject("face_forward") }
            if pose.leftEyeClosed >= 0.65 && pose.rightEyeClosed >= 0.65 {
                blinkSamples += 1; blinkClosed = blinkClosed || blinkSamples >= 2
                since = nil; samples = 0
                return CapturePoseProgress(fraction: blinkClosed ? 0.6 : 0.3, feedback: "open_eyes", complete: false)
            }
            guard blinkClosed, pose.leftEyeClosed <= 0.35, pose.rightEyeClosed <= 0.35 else { return reject("follow_prompt") }
            let held = hold(milliseconds)
            return CapturePoseProgress(fraction: 0.6 + 0.4 * held, feedback: "hold_still", complete: held == 1)
        }
        let angle: Double
        switch prompt { case .turnRight: angle = yaw; case .turnLeft: angle = -yaw; case .lookUp: angle = pitch; default: angle = -pitch }
        let cross = prompt == .turnRight || prompt == .turnLeft ? pitch : yaw
        guard abs(cross) <= 12, pose.leftEyeClosed <= 0.5, pose.rightEyeClosed <= 0.5 else { return reject("follow_prompt") }
        if abs(angle - target) > tolerance {
            reset()
            return CapturePoseProgress(fraction: angle > target + tolerance ? 0 : max(0,min(0.7,angle / target * 0.7)), feedback: "follow_prompt", complete: false)
        }
        let held = hold(milliseconds)
        return CapturePoseProgress(fraction: 0.7 + 0.3 * held, feedback: "hold_still", complete: held == 1)
    }

    private mutating func hold(_ at: Double) -> Double {
        if since == nil { since = at }; samples += 1
        return min((at - (since ?? at)) / duration, Double(samples) / 4, 1)
    }
}
