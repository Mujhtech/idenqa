import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

/// Session-oriented capture journey.
///
/// `start` attaches a native bootstrap credential to one capture session and
/// derives a one-task-at-a-time plan. `resume` re-attaches after process death
/// using only the securely stored minimum references. All consequential server
/// operations are idempotent and safe to retry with the same key.
public actor CaptureJourney {
    public nonisolated let configuration: CaptureConfiguration
    public nonisolated let capabilities: CaptureCapabilities

    private let tokenStore: any CaptureTokenStore
    private let referenceStore: any CaptureJourneyStore
    private let proofKey: any NativeProofKey
    private let attestationProvider: (any NativeAttestationProvider)?
    private let temporaryFiles: any CaptureTemporaryFileStore
    private let screenProtection: any CaptureScreenProtection
    private let time: any CaptureTimeSource
    private let idempotencyKeyFactory: @Sendable (String) -> String
    private let client: IdenqaClient

    private var session: CaptureSessionDocument?
    private var reference: CaptureJourneyReference?
    private var plan: CapturePlan?
    private var completions: [CaptureCompletionDocument] = []
    private var progressETag: String?
    private var guidance: CaptureGuidance = .none
    private var status: CaptureJourneyStatus = .idle

    public init(
        configuration: CaptureConfiguration,
        capabilities: CaptureCapabilities,
        tokenStore: any CaptureTokenStore,
        referenceStore: any CaptureJourneyStore,
        proofKey: any NativeProofKey,
        attestationProvider: (any NativeAttestationProvider)? = nil,
        transport: any HTTPTransport = URLSessionTransport(),
        realtime: any RealtimeTransport = URLSessionRealtimeTransport(),
        temporaryFiles: (any CaptureTemporaryFileStore)? = nil,
        screenProtection: any CaptureScreenProtection = NoopCaptureScreenProtection(),
        time: any CaptureTimeSource = SystemCaptureTimeSource(),
        idempotencyKeyFactory: @escaping @Sendable (String) -> String = { prefix in
            "\(prefix)_\(UUID().uuidString)"
        }
    ) throws {
        self.client = try IdenqaClient(
            baseURL: configuration.coreURL,
            tokenStore: tokenStore,
            transport: transport,
            realtime: realtime
        )
        self.temporaryFiles = try temporaryFiles ?? FileManagerCaptureTemporaryFileStore(
            namespace: configuration.installationID,
            maximumFileBytes: configuration.maximumUploadBytes,
            maximumTotalBytes: configuration.maximumTemporaryBytes
        )
        self.configuration = configuration
        self.capabilities = try capabilities.withCurrentAvailability()
        self.tokenStore = tokenStore
        self.referenceStore = referenceStore
        self.proofKey = proofKey
        self.attestationProvider = attestationProvider
        self.screenProtection = screenProtection
        self.time = time
        self.idempotencyKeyFactory = idempotencyKeyFactory
    }

    // MARK: - Start and resume

    /// Creates or attaches a capture session from an existing verification
    /// bootstrap credential. The credential is stored only through the secure
    /// token store.
    @discardableResult
    public func start(bootstrapToken: String) async throws -> CaptureJourneySnapshot {
        guard !bootstrapToken.isEmpty, !bootstrapToken.contains("\n"), !bootstrapToken.contains("\r") else {
            throw IdenqaError.invalidConfiguration
        }
        status = .starting
        do {
            try await tokenStore.write(bootstrapToken)
            _ = try await temporaryFiles.purgeAll()
            let identity = try NativeBootstrapIdentity(
                applicationID: configuration.applicationID,
                key: proofKey,
                attestationProvider: attestationProvider
            )
            let detail = try await client.bootstrapDetail(
                capabilities: capabilities.advertisement(),
                identity: identity,
                now: time.now()
            )
            session = detail
            try await attach(detail)
            return snapshot()
        } catch {
            status = reference == nil ? .idle : status
            throw mapFailure(error)
        }
    }

    /// Re-attaches the persisted journey after process death. Re-attachment is
    /// read-only against Core and safe to repeat.
    @discardableResult
    public func resume() async throws -> CaptureJourneySnapshot {
        guard let stored = try await referenceStore.read() ?? reference else { throw IdenqaError.notStarted }
        reference = stored
        guard let token = try await tokenStore.read(), !token.isEmpty else { throw IdenqaError.invalidConfiguration }
        status = .starting
        do {
            _ = try await temporaryFiles.purgeAll()
            let detail = try await client.getSession()
            session = detail
            if isTerminal(detail.state) {
                status = journeyStatus(detail.state)
                guidance = .none
                return snapshot()
            }
            try await attach(detail)
            return snapshot()
        } catch {
            throw mapFailure(error)
        }
    }

    /// Re-reads authoritative session and accepted progress without discarding retries.
    @discardableResult
    public func refresh() async throws -> CaptureJourneySnapshot {
        guard reference != nil || session != nil else { throw IdenqaError.notStarted }
        do {
            let detail = try await client.getSession()
            session = detail
            if isTerminal(detail.state) {
                status = journeyStatus(detail.state)
                guidance = .none
                return snapshot()
            }
            try await attach(detail)
            if guidance.code != .backgrounded && guidance.code != .interrupted
                && guidance.code != .networkUnavailable && guidance.code != .secureScreenCaptured
                && guidance.code != .cameraPermissionDenied {
                guidance = .none
            }
            return snapshot()
        } catch {
            throw mapFailure(error)
        }
    }

    // MARK: - Capture

    /// Uploads one captured artefact for the current task. The artefact is
    /// staged in bounded app-private temporary storage and removed on every path.
    @discardableResult
    public func submit(taskID: String, artifact: CapturedArtifact, method: String) async throws -> CaptureJourneySnapshot {
        guard let session = session, let plan = plan, let task = plan.tasks.first(where: { $0.id == taskID }) else {
            throw IdenqaError.notStarted
        }
        guard plan.currentTask?.id == taskID else { throw IdenqaError.stateConflict }
        guard task.methodOptions.contains(method),
              capabilities.declaredMethods.contains(method),
              capabilities.availableMethods.contains(method) else {
            throw IdenqaError.noCompatibleMethod
        }
        guard artifact.acquisitionMethod == method, !artifact.bytes.isEmpty,
              artifact.bytes.count <= configuration.maximumUploadBytes else {
            throw IdenqaError.invalidConfiguration
        }
        status = .uploading
        let staged = try await temporaryFiles.stage(artifact, category: "evidence", now: time.now())
        do {
            let body = try await temporaryFiles.data(for: staged)
            let digest = CaptureDigest.sha256Hex(body)
            let key = try await idempotencyKey(storageKey: "upload.issue:\(task.id)", prefix: "capture_upload")
            let input = CaptureEvidenceUploadCreate(
                requirementKey: task.requirementKey,
                artefact: task.artefact,
                acquisitionMethod: method,
                fallbackCondition: task.fallbackCondition?.rawValue,
                expectedBytes: body.count,
                expectedDigest: "sha256:\(digest)",
                mediaType: artifact.contentType,
                region: session.region
            )
            let issued = try await client.createEvidenceUpload(input, idempotencyKey: key)
            guard let etag = issued.etag else { throw IdenqaError.invalidResponse }
            let accepted = try await client.uploadEvidence(
                uploadID: issued.upload.id,
                contentType: artifact.contentType,
                body: body,
                etag: etag,
                digestHex: digest
            )
            try await temporaryFiles.remove(staged)
            if accepted.upload.state == "rejected" || accepted.upload.state == "expired" {
                throw IdenqaError.stateConflict
            }
            guard accepted.upload.state == "accepted" else {
                guidance = CaptureGuidance(
                    code: .confirmationPending,
                    message: guidanceMessage(.confirmationPending),
                    recovery: .refresh
                )
                status = journeyStatus(session.state)
                return snapshot()
            }
            try await refreshProgress()
            if plan.currentTask?.id == taskID {
                guidance = CaptureGuidance(
                    code: .confirmationPending,
                    message: guidanceMessage(.confirmationPending),
                    recovery: .refresh
                )
            } else {
                guidance = .none
            }
            status = journeyStatus(session.state)
            return snapshot()
        } catch {
            try? await temporaryFiles.remove(staged)
            status = journeyStatus(session.state)
            throw mapFailure(error)
        }
    }

    /// Records a failed live capture attempt and applies only policy-approved
    /// `capture_failed` fallbacks.
    @discardableResult
    public func captureFailed(taskID: String, reason: CaptureGuidanceCode = .qualityRejected) async throws -> CaptureJourneySnapshot {
        guard let session = session, let plan = plan, let reference = reference, plan.currentTask?.id == taskID else {
            throw IdenqaError.stateConflict
        }
        var updated = reference
        if !updated.failedTaskIDs.contains(taskID) { updated.failedTaskIDs.append(taskID) }
        self.reference = updated
        let rebuilt = try CapturePlanBuilder.build(
            session: session,
            capabilities: capabilities,
            preferences: configuration.preferredMethods,
            progress: completions,
            captureFailedTaskIDs: Set(updated.failedTaskIDs)
        )
        self.plan = rebuilt
        try await referenceStore.write(updated)
        guidance = CaptureGuidance(
            code: reason,
            message: guidanceMessage(reason),
            recovery: .retry
        )
        status = journeyStatus(session.state)
        return snapshot()
    }

    // MARK: - Cancellation

    /// Idempotent server-side cancellation. Retrying with the same key replays
    /// the original receipt; the key is persisted across process death.
    @discardableResult
    public func cancel(idempotencyKey providedKey: String? = nil) async throws -> CaptureCancellation {
        guard var stored = try await referenceStore.read() ?? reference else { throw IdenqaError.notStarted }
        reference = stored
        let key = providedKey ?? stored.idempotencyKeys["cancel"] ?? idempotencyKeyFactory("capture_cancel")
        _ = try structuredString(key)
        stored.idempotencyKeys["cancel"] = key
        reference = stored
        try await referenceStore.write(stored)
        let expected = session?.version ?? stored.sessionVersion
        do {
            let receipt = try await client.cancel(expectedVersion: expected, idempotencyKey: key)
            status = .cancelled
            guidance = .none
            return CaptureCancellation(document: receipt)
        } catch let error as IdenqaError where error == .stateConflict {
            if let detail = try? await client.getSession() {
                session = detail
                if isTerminal(detail.state) { status = journeyStatus(detail.state) }
            }
            throw error
        } catch {
            throw mapFailure(error)
        }
    }

    // MARK: - Local data

    /// Removes every local credential, reference, cached progress value, and
    /// temporary file, then verifies the result. Idempotent.
    @discardableResult
    public func clearLocalData() async throws -> CaptureClearReport {
        var removed = 0
        do { removed = try await temporaryFiles.purgeAll() } catch { removed = 0 }
        try await tokenStore.clear()
        try await referenceStore.clear()
        await proofKey.clear()
        session = nil
        reference = nil
        plan = nil
        completions = []
        progressETag = nil
        guidance = .none
        status = .idle

        let tokenCleared = ((try? await tokenStore.read()) ?? nil) == nil
        let referencesCleared = ((try? await referenceStore.read()) ?? nil) == nil
        let keyCleared = await proofKey.isCleared()
        return CaptureClearReport(
            tokenCleared: tokenCleared,
            referencesCleared: referencesCleared,
            temporaryFilesRemoved: removed,
            proofKeyCleared: keyCleared,
            progressCacheCleared: progressETag == nil && completions.isEmpty
        )
    }

    // MARK: - Snapshot and capabilities

    public func currentSnapshot() -> CaptureJourneySnapshot { snapshot() }

    public func getCapabilities() -> CaptureCapabilities { capabilities }

    // MARK: - Host lifecycle

    /// Backgrounding never discards the persisted reference or in-flight work.
    public func applicationEnteredBackground() -> CaptureJourneySnapshot {
        if canCancel {
            guidance = CaptureGuidance(
                code: .backgrounded,
                message: guidanceMessage(.backgrounded),
                recovery: .wait
            )
        }
        return snapshot()
    }

    /// Foreground re-attachment is read-only and idempotent.
    @discardableResult
    public func applicationEnteredForeground() async throws -> CaptureJourneySnapshot {
        guard reference != nil else { return snapshot() }
        if guidance.code == .backgrounded {
            guidance = .none
        }
        return try await refresh()
    }

    public func captureInterruptionBegan() -> CaptureJourneySnapshot {
        if canCancel {
            guidance = CaptureGuidance(
                code: .interrupted,
                message: guidanceMessage(.interrupted),
                recovery: .retry
            )
        }
        return snapshot()
    }

    public func captureInterruptionEnded() -> CaptureJourneySnapshot {
        if guidance.code == .interrupted { guidance = .none }
        return snapshot()
    }

    public func cameraPermissionChanged(granted: Bool) -> CaptureJourneySnapshot {
        if granted {
            if guidance.code == .cameraPermissionDenied { guidance = .none }
        } else {
            guidance = CaptureGuidance(
                code: .cameraPermissionDenied,
                message: guidanceMessage(.cameraPermissionDenied),
                recovery: .openSettings
            )
        }
        return snapshot()
    }

    public func cameraBecameUnavailable() -> CaptureJourneySnapshot {
        guidance = CaptureGuidance(
            code: .cameraUnavailable,
            message: guidanceMessage(.cameraUnavailable),
            recovery: .retry
        )
        return snapshot()
    }

    public func networkBecameUnavailable() -> CaptureJourneySnapshot {
        guidance = CaptureGuidance(
            code: .networkUnavailable,
            message: guidanceMessage(.networkUnavailable),
            recovery: .retry
        )
        return snapshot()
    }

    public func networkBecameAvailable() -> CaptureJourneySnapshot {
        if guidance.code == .networkUnavailable { guidance = .none }
        return snapshot()
    }

    /// Applies platform sensitive-screen protection while a capture preview is
    /// visible. Returns the honest availability of the platform surface.
    @discardableResult
    public func applySensitiveScreenProtection() async -> Bool {
        await screenProtection.applyProtection()
        return screenProtection.isAvailable
    }

    public func removeSensitiveScreenProtection() async {
        await screenProtection.removeProtection()
    }

    public func screenCaptureState() async -> Bool {
        await screenProtection.isCaptured()
    }

    public func screenCaptureChanged(isCaptured: Bool) -> CaptureJourneySnapshot {
        if isCaptured {
            guidance = CaptureGuidance(
                code: .secureScreenCaptured,
                message: guidanceMessage(.secureScreenCaptured),
                recovery: .wait
            )
        } else if guidance.code == .secureScreenCaptured {
            guidance = .none
        }
        return snapshot()
    }

    // MARK: - Internals

    private func attach(_ detail: CaptureSessionDocument) async throws {
        if let pinned = configuration.captureProfileID, pinned != detail.profileID {
            throw IdenqaError.stateConflict
        }
        if let pinned = configuration.region, pinned != detail.region {
            throw IdenqaError.stateConflict
        }
        if reference == nil, let stored = try? await referenceStore.read() {
            reference = stored
        }
        let progressResult = try await client.getProgress()
        guard let progress = progressResult.progress, progress.verificationID == detail.id else {
            throw IdenqaError.invalidResponse
        }
        completions = progress.completions
        progressETag = progressResult.etag
        let failed = Set(reference?.failedTaskIDs ?? [])
        let rebuilt = try CapturePlanBuilder.build(
            session: detail,
            capabilities: capabilities,
            preferences: configuration.preferredMethods,
            progress: progress.completions,
            captureFailedTaskIDs: failed
        )
        plan = rebuilt
        session = detail
        let existingKeys = reference?.idempotencyKeys ?? [:]
        let updated = CaptureJourneyReference(
            verificationID: detail.id,
            sessionVersion: detail.version,
            region: detail.region,
            profileRevision: detail.profileRevision,
            profileDigest: detail.profileDigest,
            expiresAt: detail.expiresAt,
            completedTaskIDs: rebuilt.completedTaskIDs.sorted(),
            failedTaskIDs: failed.sorted(),
            idempotencyKeys: existingKeys
        )
        reference = updated
        try await referenceStore.write(updated)
        status = journeyStatus(detail.state)
    }

    private func refreshProgress() async throws {
        guard let session else { return }
        let result = try await client.getProgress(ifNoneMatch: progressETag)
        if result.notModified { return }
        guard let progress = result.progress, progress.verificationID == session.id else {
            throw IdenqaError.invalidResponse
        }
        completions = progress.completions
        progressETag = result.etag
        let failed = Set(reference?.failedTaskIDs ?? [])
        let rebuilt = try CapturePlanBuilder.build(
            session: session,
            capabilities: capabilities,
            preferences: configuration.preferredMethods,
            progress: progress.completions,
            captureFailedTaskIDs: failed
        )
        plan = rebuilt
        if var stored = reference {
            stored.completedTaskIDs = rebuilt.completedTaskIDs.sorted()
            stored.sessionVersion = session.version
            reference = stored
            try await referenceStore.write(stored)
        }
    }

    private func idempotencyKey(storageKey: String, prefix: String) async throws -> String {
        guard var stored = reference else { throw IdenqaError.notStarted }
        if let existing = stored.idempotencyKeys[storageKey] {
            _ = try structuredString(existing)
            return existing
        }
        let key = idempotencyKeyFactory(prefix)
        _ = try structuredString(key)
        stored.idempotencyKeys[storageKey] = key
        reference = stored
        try? await referenceStore.write(stored)
        return key
    }

    private func mapFailure(_ error: Error) -> IdenqaError {
        if let urlError = error as? URLError {
            switch urlError.code {
            case .notConnectedToInternet, .networkConnectionLost, .cannotFindHost,
                 .cannotConnectToHost, .timedOut, .dataNotAllowed, .internationalRoamingOff:
                guidance = CaptureGuidance(
                    code: .networkUnavailable,
                    message: guidanceMessage(.networkUnavailable),
                    recovery: .retry
                )
                return .networkUnavailable
            default:
                break
            }
        }
        guard let idenqa = error as? IdenqaError else { return .invalidResponse }
        switch idenqa {
        case .noCompatibleMethod:
            guidance = CaptureGuidance(
                code: .noCompatibleMethod,
                message: guidanceMessage(.noCompatibleMethod),
                recovery: .chooseAnotherMethod
            )
        case .temporaryStorage, .transport:
            if guidance.code == .none {
                guidance = CaptureGuidance(
                    code: .uploadRejected,
                    message: guidanceMessage(.uploadRejected),
                    recovery: .retry
                )
            }
        default:
            break
        }
        return idenqa
    }

    private func snapshot() -> CaptureJourneySnapshot {
        CaptureJourneySnapshot(
            status: status,
            verificationID: session?.id ?? reference?.verificationID,
            sessionVersion: session?.version ?? reference?.sessionVersion,
            region: session?.region ?? reference?.region,
            expiresAt: session?.expiresAt ?? reference?.expiresAt,
            currentTask: plan?.currentTask,
            completedTaskCount: plan?.completedTaskIDs.count ?? 0,
            remainingTaskCount: plan.map { $0.tasks.count - $0.completedTaskIDs.count } ?? 0,
            guidance: guidance
        )
    }

    private var canCancel: Bool {
        switch status {
        case .idle, .completed, .cancelled, .expired, .failed:
            return false
        default:
            return reference != nil
        }
    }

    private func isTerminal(_ state: String) -> Bool {
        ["completed", "cancelled", "expired", "failed"].contains(state)
    }

    private func journeyStatus(_ state: String) -> CaptureJourneyStatus {
        switch state {
        case "created", "collecting": return .captureRequired
        case "awaiting_input": return .awaitingInput
        case "processing": return .processing
        case "awaiting_external": return .awaitingExternal
        case "manual_review": return .manualReview
        case "completed": return .completed
        case "cancelled": return .cancelled
        case "expired": return .expired
        case "failed": return .failed
        default: return .blocked
        }
    }

    func guidanceMessage(_ code: CaptureGuidanceCode) -> String {
        switch code {
        case .none: return ""
        case .cameraPermissionDenied: return "Allow camera access to continue. You can enable it in Settings."
        case .cameraUnavailable: return "The camera is not available right now. Try again or choose another option."
        case .networkUnavailable: return "Check your connection and try again. Your progress is saved."
        case .backgrounded: return "Your progress is saved. Return when you are ready to continue."
        case .interrupted: return "Capture was interrupted. Your progress is saved."
        case .qualityRejected: return "That capture did not meet the required quality. Try again."
        case .captureTimeout: return "Capture took too long. Try again."
        case .secureScreenCaptured: return "Screen recording or mirroring is active. Stop it to protect your information."
        case .noCompatibleMethod: return "No approved capture option is available on this device."
        case .uploadRejected: return "The upload was not accepted. Try again."
        case .confirmationPending: return "Finishing this step. Refresh in a moment."
        case .profileMismatch: return "This capture link is no longer valid."
        }
    }
}
