import Foundation

public enum IdenqaError: Error, Equatable, Sendable {
    case invalidConfiguration
    case invalidResponse
    case transport(status: Int)
    case stateConflict
    case secureStorage
    case hardwareSecurityUnavailable
    case cameraPermissionDenied
    case cameraUnavailable
    case captureQuality
    case captureTimeout
    case notStarted
    case notFound
    case unauthenticated
    case networkUnavailable
    case temporaryStorage
    case noCompatibleMethod
}

public protocol CaptureTokenStore: Sendable {
    func read() async throws -> String?
    func write(_ token: String) async throws
    func clear() async throws
}

public struct CapturedArtifact: Sendable {
    public let bytes: Data
    public let contentType: String
    public let acquisitionMethod: String

    public init(bytes: Data, contentType: String, acquisitionMethod: String) {
        self.bytes = bytes
        self.contentType = contentType
        self.acquisitionMethod = acquisitionMethod
    }
}

extension CapturedArtifact: CustomStringConvertible, CustomDebugStringConvertible {
    /// Evidence bytes never appear in descriptions, logs, or crash reports.
    public var description: String {
        "CapturedArtifact(contentType: \(contentType), acquisitionMethod: \(acquisitionMethod), bytes: <redacted>)"
    }

    public var debugDescription: String { description }
}

public enum CaptureCamera: String, Codable, Sendable {
    case front
    case back
}

public enum LivenessPrompt: String, Codable, Sendable {
    case neutral
    case turnLeft = "turn_left"
    case turnRight = "turn_right"
    case lookUp = "look_up"
    case lookDown = "look_down"
    case blink
}

public struct CaptureQualityPolicy: Equatable, Sendable {
    public let minimumWidth: Int
    public let minimumHeight: Int
    public let maximumBytes: Int
    public let minimumBrightness: Double?
    public let maximumBrightness: Double?
    public let minimumContrast: Double?
    public let minimumSharpness: Double?
    public let maximumGlare: Double?
    public let requiredFaceCount: Int?

    public init(
        minimumWidth: Int,
        minimumHeight: Int,
        maximumBytes: Int,
        minimumBrightness: Double? = nil,
        maximumBrightness: Double? = nil,
        minimumContrast: Double? = nil,
        minimumSharpness: Double? = nil,
        maximumGlare: Double? = nil,
        requiredFaceCount: Int? = nil
    ) {
        self.minimumWidth = minimumWidth
        self.minimumHeight = minimumHeight
        self.maximumBytes = maximumBytes
        self.minimumBrightness = minimumBrightness
        self.maximumBrightness = maximumBrightness
        self.minimumContrast = minimumContrast
        self.minimumSharpness = minimumSharpness
        self.maximumGlare = maximumGlare
        self.requiredFaceCount = requiredFaceCount
    }
}

public struct CaptureQualityMeasurement: Equatable, Sendable {
    public let width: Int
    public let height: Int
    public let byteCount: Int
    public let brightness: Double
    public let contrast: Double
    public let sharpness: Double
    public let glare: Double
    public let faceCount: Int

    public init(width: Int, height: Int, byteCount: Int, brightness: Double, contrast: Double, sharpness: Double, glare: Double, faceCount: Int) {
        self.width = width
        self.height = height
        self.byteCount = byteCount
        self.brightness = brightness
        self.contrast = contrast
        self.sharpness = sharpness
        self.glare = glare
        self.faceCount = faceCount
    }

    public func failures(against policy: CaptureQualityPolicy) -> [String] {
        var result: [String] = []
        if width <= 0 || height <= 0 || byteCount <= 0 || faceCount < 0 ||
            ![brightness, contrast, sharpness, glare].allSatisfy({ $0.isFinite && (0...1).contains($0) }) {
            return ["invalid_measurement"]
        }
        if width < policy.minimumWidth || height < policy.minimumHeight { result.append("dimensions") }
        if byteCount > policy.maximumBytes { result.append("size") }
        if let value = policy.minimumBrightness, brightness < value { result.append("too_dark") }
        if let value = policy.maximumBrightness, brightness > value { result.append("too_bright") }
        if let value = policy.minimumContrast, contrast < value { result.append("low_contrast") }
        if let value = policy.minimumSharpness, sharpness < value { result.append("blur") }
        if let value = policy.maximumGlare, glare > value { result.append("glare") }
        if let value = policy.requiredFaceCount, faceCount != value { result.append("face_count") }
        return result
    }
}

public struct LivenessChallenge: Equatable, Sendable {
    public let id: String
    public let prompt: LivenessPrompt
    public let maximumDuration: Duration
    public let pose: CapturePosePolicy?

    public init(id: String, prompt: LivenessPrompt, maximumDuration: Duration, pose: CapturePosePolicy? = nil) {
        self.id = id
        self.prompt = prompt
        self.maximumDuration = maximumDuration
        self.pose = pose
    }
}

public struct CaptureRequirement: Equatable, Sendable {
    public let id: String
    public let evidenceType: String
    public let artefact: String
    public let camera: CaptureCamera
    public let quality: CaptureQualityPolicy
    public let challenges: [LivenessChallenge]

    public init(id: String, evidenceType: String, artefact: String, camera: CaptureCamera, quality: CaptureQualityPolicy, challenges: [LivenessChallenge] = []) {
        self.id = id
        self.evidenceType = evidenceType
        self.artefact = artefact
        self.camera = camera
        self.quality = quality
        self.challenges = challenges
    }
}

public struct AcquiredFrame: Sendable {
    public let capturedAt: Date
    public let challengeID: String?
    public let artifact: CapturedArtifact
    public let quality: CaptureQualityMeasurement

    public init(challengeID: String?, artifact: CapturedArtifact, quality: CaptureQualityMeasurement, capturedAt: Date = Date()) {
        self.capturedAt = capturedAt
        self.challengeID = challengeID
        self.artifact = artifact
        self.quality = quality
    }
}

public protocol CaptureQualityAssessor: Sendable {
    func assess(_ artifact: CapturedArtifact) async throws -> CaptureQualityMeasurement
}

public protocol ChallengePresenter: Sendable {
    func present(_ challenge: LivenessChallenge) async throws
}

public protocol RawCaptureSource: Sendable {
    func capture() async throws -> CapturedArtifact
}

public struct CapabilityAdvertisement: Codable, Equatable, Sendable {
    public let platform: String
    public let sdkVersion: String
    public let implementedMethods: [String]
    public let currentlyAvailableMethods: [String]

    public init(platform: String = "ios", sdkVersion: String, implementedMethods: [String], currentlyAvailableMethods: [String]) {
        self.platform = platform
        self.sdkVersion = sdkVersion
        self.implementedMethods = implementedMethods
        self.currentlyAvailableMethods = currentlyAvailableMethods
    }
}

public struct CaptureSession: Codable, Equatable, Sendable {
    public let id: String
    public let state: String
    public let version: Int64
    public let region: String
    public let expiresAt: Date

    enum CodingKeys: String, CodingKey {
        case id, state, version, region
        case expiresAt = "expires_at"
    }
}

public struct RealtimeEvent: Codable, Equatable, Sendable {
    public let sequence: UInt64
    public let type: String
    public let sessionVersion: Int64

    enum CodingKeys: String, CodingKey {
        case sequence, type
        case sessionVersion = "session_version"
    }
}

public actor CaptureSessionState {
    public private(set) var session: CaptureSession
    public private(set) var lastSequence: UInt64 = 0

    public init(session: CaptureSession) { self.session = session }

    public func apply(_ event: RealtimeEvent) throws {
        guard event.sequence == lastSequence + 1, event.sessionVersion >= session.version else {
            throw IdenqaError.stateConflict
        }
        lastSequence = event.sequence
        session = CaptureSession(id: session.id, state: event.type, version: event.sessionVersion, region: session.region, expiresAt: session.expiresAt)
    }
}
