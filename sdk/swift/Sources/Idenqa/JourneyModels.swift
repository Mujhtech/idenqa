import Foundation

// MARK: - Wire documents

struct CaptureProfileRegistry: Codable, Equatable, Sendable {
    let schemaVersion: Int
    let revision: Int
    let digest: String

    enum CodingKeys: String, CodingKey {
        case revision, digest
        case schemaVersion = "schema_version"
    }
}

struct CaptureAcquisition: Codable, Equatable, Sendable {
    let strategy: String
    let methods: [String]
}

struct CaptureFallback: Codable, Equatable, Sendable {
    let on: [String]
    let acquisition: CaptureAcquisition
}

struct CaptureRequirementDocument: Codable, Equatable, Sendable {
    let key: String
    let purpose: String
    let evidenceType: String
    let artefacts: [String]
    let acquisition: CaptureAcquisition
    let requiredAssurances: [String]
    let fallbacks: [CaptureFallback]

    enum CodingKeys: String, CodingKey {
        case key, purpose, artefacts, acquisition, fallbacks
        case evidenceType = "evidence_type"
        case requiredAssurances = "required_assurances"
    }
}

struct CaptureProfileDocument: Codable, Equatable, Sendable {
    let schemaVersion: Int
    let registry: CaptureProfileRegistry
    let requirements: [CaptureRequirementDocument]

    enum CodingKeys: String, CodingKey {
        case registry, requirements
        case schemaVersion = "schema_version"
    }
}

struct CaptureSessionDocument: Codable, Equatable, Sendable {
    let id: String
    let state: String
    let version: Int64
    let profileID: String
    let profileRevision: Int
    let profileDigest: String
    let region: String
    let requirements: CaptureProfileDocument
    let expiresAt: Date

    enum CodingKeys: String, CodingKey {
        case id, state, version, region, requirements
        case profileID = "profile_id"
        case profileRevision = "profile_revision"
        case profileDigest = "profile_digest"
        case expiresAt = "expires_at"
    }
}

struct CaptureCompletionDocument: Codable, Equatable, Sendable {
    let uploadID: String
    let evidenceID: String
    let requirementKey: String
    let evidenceType: String
    let artefact: String
    let acquisitionMethod: String
    let fallbackCondition: String?

    enum CodingKeys: String, CodingKey {
        case uploadID = "upload_id"
        case evidenceID = "evidence_id"
        case requirementKey = "requirement_key"
        case evidenceType = "evidence_type"
        case artefact
        case acquisitionMethod = "acquisition_method"
        case fallbackCondition = "fallback_condition"
    }
}

struct CaptureProgressDocument: Codable, Equatable, Sendable {
    let verificationID: String
    let completions: [CaptureCompletionDocument]

    enum CodingKeys: String, CodingKey {
        case verificationID = "verification_id"
        case completions
    }
}

struct CaptureCancellationDocument: Codable, Equatable, Sendable {
    let eventID: String
    let verificationID: String
    let state: String
    let version: Int64
    let occurredAt: Date

    enum CodingKeys: String, CodingKey {
        case state, version
        case eventID = "event_id"
        case verificationID = "verification_id"
        case occurredAt = "occurred_at"
    }
}

struct EvidenceUploadDocument: Codable, Equatable, Sendable {
    let id: String
    let evidenceID: String
    let state: String
    let version: Int64
    let attempt: Int
    let requirementKey: String
    let evidenceType: String
    let artefact: String
    let acquisitionMethod: String
    let fallbackCondition: String?
    let assurances: [String]
    let allowedMediaTypes: [String]
    let maximumBytes: Int64
    let expectedBytes: Int64
    let mediaType: String
    let region: String
    let createdAt: Date
    let updatedAt: Date
    let expiresAt: Date
    let acceptedAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, state, version, attempt, artefact, assurances, region
        case evidenceID = "evidence_id"
        case requirementKey = "requirement_key"
        case evidenceType = "evidence_type"
        case acquisitionMethod = "acquisition_method"
        case fallbackCondition = "fallback_condition"
        case allowedMediaTypes = "allowed_media_types"
        case maximumBytes = "maximum_bytes"
        case expectedBytes = "expected_bytes"
        case mediaType = "media_type"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
        case expiresAt = "expires_at"
        case acceptedAt = "accepted_at"
    }
}

struct CaptureEvidenceUploadCreate: Codable, Equatable, Sendable {
    let requirementKey: String
    let artefact: String
    let acquisitionMethod: String
    let fallbackCondition: String?
    let expectedBytes: Int
    let expectedDigest: String
    let mediaType: String
    let region: String

    enum CodingKeys: String, CodingKey {
        case artefact, region
        case requirementKey = "requirement_key"
        case acquisitionMethod = "acquisition_method"
        case fallbackCondition = "fallback_condition"
        case expectedBytes = "expected_bytes"
        case expectedDigest = "expected_digest"
        case mediaType = "media_type"
    }
}

struct CaptureCancelCommand: Encodable, Sendable {
    let expectedVersion: Int64

    enum CodingKeys: String, CodingKey {
        case expectedVersion = "expected_version"
    }
}

// MARK: - Public plan and task values

/// A policy-approved fallback reason. Unknown values are never invented locally.
public enum CaptureFallbackCondition: String, Codable, Equatable, Sendable {
    case capabilityUnavailable = "capability_unavailable"
    case methodUnavailable = "method_unavailable"
    case captureFailed = "capture_failed"
}

/// One capture step. Exactly one task is current at a time.
public struct CaptureTask: Equatable, Sendable {
    public let id: String
    public let requirementKey: String
    public let evidenceType: String
    public let artefact: String
    public let methodOptions: [String]
    public let fallbackCondition: CaptureFallbackCondition?
    public let requiredAssurances: [String]

    public init(
        id: String,
        requirementKey: String,
        evidenceType: String,
        artefact: String,
        methodOptions: [String],
        fallbackCondition: CaptureFallbackCondition?,
        requiredAssurances: [String] = []
    ) {
        self.id = id
        self.requirementKey = requirementKey
        self.evidenceType = evidenceType
        self.artefact = artefact
        self.methodOptions = methodOptions
        self.fallbackCondition = fallbackCondition
        self.requiredAssurances = requiredAssurances
    }
}

/// Ordered plan derived from the immutable session snapshot and this device's capabilities.
public struct CapturePlan: Equatable, Sendable {
    public let verificationID: String
    public let profileRevision: Int
    public let profileDigest: String
    public let tasks: [CaptureTask]
    public let completedTaskIDs: Set<String>

    public init(
        verificationID: String,
        profileRevision: Int,
        profileDigest: String,
        tasks: [CaptureTask],
        completedTaskIDs: Set<String>
    ) {
        self.verificationID = verificationID
        self.profileRevision = profileRevision
        self.profileDigest = profileDigest
        self.tasks = tasks
        self.completedTaskIDs = completedTaskIDs
    }

    public var pendingTasks: [CaptureTask] { tasks.filter { !completedTaskIDs.contains($0.id) } }
    public var currentTask: CaptureTask? { pendingTasks.first }
}

// MARK: - Journey values

public enum CaptureJourneyStatus: String, Equatable, Sendable {
    case idle
    case starting
    case captureRequired = "capture_required"
    case uploading
    case awaitingInput = "awaiting_input"
    case processing
    case awaitingExternal = "awaiting_external"
    case manualReview = "manual_review"
    case completed
    case cancelled
    case expired
    case failed
    case blocked
}

public enum CaptureGuidanceCode: String, Equatable, Sendable {
    case none
    case cameraPermissionDenied = "camera_permission_denied"
    case cameraUnavailable = "camera_unavailable"
    case networkUnavailable = "network_unavailable"
    case backgrounded
    case interrupted
    case qualityRejected = "quality_rejected"
    case captureTimeout = "capture_timeout"
    case secureScreenCaptured = "secure_screen_captured"
    case noCompatibleMethod = "no_compatible_method"
    case uploadRejected = "upload_rejected"
    case confirmationPending = "confirmation_pending"
    case profileMismatch = "profile_mismatch"
}

public enum CaptureRecoveryAction: String, Equatable, Sendable {
    case none
    case retry
    case openSettings = "open_settings"
    case chooseAnotherMethod = "choose_another_method"
    case wait
    case refresh
}

/// Subject-safe guidance for the host UI. Cancellation stays reachable in every non-terminal state.
public struct CaptureGuidance: Equatable, Sendable {
    public let code: CaptureGuidanceCode
    public let message: String
    public let recovery: CaptureRecoveryAction
    public let cancelAvailable: Bool

    public init(code: CaptureGuidanceCode, message: String, recovery: CaptureRecoveryAction, cancelAvailable: Bool = true) {
        self.code = code
        self.message = message
        self.recovery = recovery
        self.cancelAvailable = cancelAvailable
    }

    public static let none = CaptureGuidance(code: .none, message: "", recovery: .none)
}

/// One screen at a time: hosts render `currentTask` and never the full plan.
public struct CaptureJourneySnapshot: Equatable, Sendable {
    public let status: CaptureJourneyStatus
    public let verificationID: String?
    public let sessionVersion: Int64?
    public let region: String?
    public let expiresAt: Date?
    public let currentTask: CaptureTask?
    public let completedTaskCount: Int
    public let remainingTaskCount: Int
    public let guidance: CaptureGuidance

    public init(
        status: CaptureJourneyStatus,
        verificationID: String?,
        sessionVersion: Int64?,
        region: String?,
        expiresAt: Date?,
        currentTask: CaptureTask?,
        completedTaskCount: Int,
        remainingTaskCount: Int,
        guidance: CaptureGuidance
    ) {
        self.status = status
        self.verificationID = verificationID
        self.sessionVersion = sessionVersion
        self.region = region
        self.expiresAt = expiresAt
        self.currentTask = currentTask
        self.completedTaskCount = completedTaskCount
        self.remainingTaskCount = remainingTaskCount
        self.guidance = guidance
    }

    public static let idle = CaptureJourneySnapshot(
        status: .idle,
        verificationID: nil,
        sessionVersion: nil,
        region: nil,
        expiresAt: nil,
        currentTask: nil,
        completedTaskCount: 0,
        remainingTaskCount: 0,
        guidance: .none
    )

    public var canResume: Bool { status != .idle }
    public var canCancel: Bool {
        switch status {
        case .starting, .captureRequired, .uploading, .awaitingInput, .awaitingExternal, .manualReview, .blocked, .processing:
            return true
        case .idle, .completed, .cancelled, .expired, .failed:
            return false
        }
    }
}

// MARK: - Persisted minimum references

/// The only durable local journey state. It contains opaque references and no
/// evidence bytes, notice copy, subject data, or credentials other than the
/// separately secure-stored capture token.
public struct CaptureJourneyReference: Codable, Equatable, Sendable {
    public let verificationID: String
    public var sessionVersion: Int64
    public let region: String
    public let profileRevision: Int
    public let profileDigest: String
    public let expiresAt: Date
    public var completedTaskIDs: [String]
    public var failedTaskIDs: [String]
    public var idempotencyKeys: [String: String]

    public init(
        verificationID: String,
        sessionVersion: Int64,
        region: String,
        profileRevision: Int,
        profileDigest: String,
        expiresAt: Date,
        completedTaskIDs: [String] = [],
        failedTaskIDs: [String] = [],
        idempotencyKeys: [String: String] = [:]
    ) {
        self.verificationID = verificationID
        self.sessionVersion = sessionVersion
        self.region = region
        self.profileRevision = profileRevision
        self.profileDigest = profileDigest
        self.expiresAt = expiresAt
        self.completedTaskIDs = completedTaskIDs
        self.failedTaskIDs = failedTaskIDs
        self.idempotencyKeys = idempotencyKeys
    }
}

/// Secure persistence for the minimum journey reference. Platform implementations
/// use Keychain or Keystore-backed encrypted storage; plaintext files are forbidden.
public protocol CaptureJourneyStore: Sendable {
    func read() async throws -> CaptureJourneyReference?
    func write(_ reference: CaptureJourneyReference) async throws
    func clear() async throws
}

// MARK: - Cancellation receipt

public struct CaptureCancellation: Equatable, Sendable {
    public let eventID: String
    public let verificationID: String
    public let state: String
    public let version: Int64
    public let occurredAt: Date

    init(document: CaptureCancellationDocument) {
        eventID = document.eventID
        verificationID = document.verificationID
        state = document.state
        version = document.version
        occurredAt = document.occurredAt
    }
}

// MARK: - Clear report

public struct CaptureClearReport: Equatable, Sendable {
    public let tokenCleared: Bool
    public let referencesCleared: Bool
    public let temporaryFilesRemoved: Int
    public let proofKeyCleared: Bool
    public let progressCacheCleared: Bool

    public init(
        tokenCleared: Bool,
        referencesCleared: Bool,
        temporaryFilesRemoved: Int,
        proofKeyCleared: Bool,
        progressCacheCleared: Bool
    ) {
        self.tokenCleared = tokenCleared
        self.referencesCleared = referencesCleared
        self.temporaryFilesRemoved = temporaryFilesRemoved
        self.proofKeyCleared = proofKeyCleared
        self.progressCacheCleared = progressCacheCleared
    }

    public var allCleared: Bool {
        tokenCleared && referencesCleared && proofKeyCleared && progressCacheCleared
    }
}

// MARK: - Time source

public protocol CaptureTimeSource: Sendable {
    func now() -> Date
}

public struct SystemCaptureTimeSource: CaptureTimeSource {
    public init() {}
    public func now() -> Date { Date() }
}
