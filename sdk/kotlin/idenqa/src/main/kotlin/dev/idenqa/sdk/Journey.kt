package dev.idenqa.sdk

import java.io.IOException
import java.util.UUID
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/**
 * Session-oriented capture journey.
 *
 * `start` attaches a native bootstrap credential to one capture session and
 * derives a one-task-at-a-time plan. `resume` re-attaches after process death
 * using only the securely stored minimum references. All consequential server
 * operations are idempotent and safe to retry with the same key.
 */
class CaptureJourney(
    val configuration: CaptureConfiguration,
    initialCapabilities: CaptureCapabilities,
    private val tokenStore: CaptureTokenStore,
    private val referenceStore: CaptureJourneyStore,
    private val proofKey: NativeProofKey,
    private val attestationProvider: NativeAttestationProvider? = null,
    transport: HttpTransport = OkHttpTransport(),
    realtime: RealtimeTransport = OkHttpRealtimeTransport(),
    private val temporaryFiles: CaptureTemporaryFileStore,
    private val screenProtection: ScreenProtection = NoopScreenProtection,
    private val time: CaptureTimeSource = SystemCaptureTimeSource,
    private val idempotencyKeyFactory: (String) -> String = { prefix -> "${prefix}_${UUID.randomUUID()}" },
) {
    val captureCapabilities: CaptureCapabilities = initialCapabilities.withCurrentAvailability()

    private val client = IdenqaClient(configuration.coreUri, tokenStore, transport, realtime)
    private val mutex = Mutex()
    private var session: CaptureSessionDetail? = null
    private var reference: CaptureJourneyReference? = null
    private var plan: CapturePlan? = null
    private var completions: List<CaptureCompletion> = emptyList()
    private var progressETag: String? = null
    private var guidance: CaptureGuidance = CaptureGuidance.None
    private var status: CaptureJourneyStatus = CaptureJourneyStatus.IDLE

    // MARK: - Start and resume

    /** Creates or attaches a capture session from an existing verification bootstrap credential. */
    suspend fun start(bootstrapToken: String): CaptureJourneySnapshot = mutex.withLock {
        if (bootstrapToken.isBlank() || bootstrapToken.contains('\n') || bootstrapToken.contains('\r')) {
            throw IdenqaException.InvalidConfiguration
        }
        status = CaptureJourneyStatus.STARTING
        try {
            tokenStore.write(bootstrapToken)
            temporaryFiles.purgeAll()
            val identity = NativeBootstrapIdentity(configuration.applicationId, proofKey, attestationProvider)
            val detail = client.bootstrapDetail(captureCapabilities.advertisement(), identity, time.now())
            session = detail
            attach(detail)
            snapshot()
        } catch (error: Throwable) {
            if (reference == null) status = CaptureJourneyStatus.IDLE
            throw mapFailure(error)
        }
    }

    /** Re-attaches the persisted journey after process death. Safe to repeat. */
    suspend fun resume(): CaptureJourneySnapshot = mutex.withLock {
        val stored = referenceStore.read() ?: reference ?: throw IdenqaException.NotStarted
        reference = stored
        tokenStore.read()?.takeIf(String::isNotBlank) ?: throw IdenqaException.InvalidConfiguration
        status = CaptureJourneyStatus.STARTING
        try {
            temporaryFiles.purgeAll()
            val detail = client.getSession()
            session = detail
            if (isTerminal(detail.state)) {
                status = journeyStatus(detail.state)
                guidance = CaptureGuidance.None
                return@withLock snapshot()
            }
            attach(detail)
            snapshot()
        } catch (error: Throwable) {
            throw mapFailure(error)
        }
    }

    /** Re-reads authoritative session and accepted progress without discarding retries. */
    suspend fun refresh(): CaptureJourneySnapshot = mutex.withLock {
        if (reference == null && session == null) throw IdenqaException.NotStarted
        try {
            val detail = client.getSession()
            session = detail
            if (isTerminal(detail.state)) {
                status = journeyStatus(detail.state)
                guidance = CaptureGuidance.None
                return@withLock snapshot()
            }
            attach(detail)
            if (guidance.code !in setOf(
                    CaptureGuidanceCode.BACKGROUNDED, CaptureGuidanceCode.INTERRUPTED,
                    CaptureGuidanceCode.NETWORK_UNAVAILABLE, CaptureGuidanceCode.SECURE_SCREEN_CAPTURED,
                    CaptureGuidanceCode.CAMERA_PERMISSION_DENIED,
                )
            ) guidance = CaptureGuidance.None
            snapshot()
        } catch (error: Throwable) {
            throw mapFailure(error)
        }
    }

    // MARK: - Capture

    /** Uploads one captured artefact for the current task and removes staged bytes on every path. */
    suspend fun submit(taskId: String, artifact: CapturedArtifact, method: String): CaptureJourneySnapshot = mutex.withLock {
        val currentSession = session ?: throw IdenqaException.NotStarted
        val currentPlan = plan ?: throw IdenqaException.NotStarted
        val task = currentPlan.tasks.firstOrNull { it.id == taskId } ?: throw IdenqaException.NotStarted
        if (currentPlan.currentTask?.id != taskId) throw IdenqaException.StateConflict
        if (method !in task.methodOptions || method !in captureCapabilities.declaredMethods || method !in captureCapabilities.availableMethods) {
            throw IdenqaException.NoCompatibleMethod
        }
        if (artifact.acquisitionMethod != method || artifact.bytes().isEmpty() ||
            artifact.bytes().size > configuration.maximumUploadBytes
        ) throw IdenqaException.InvalidConfiguration
        status = CaptureJourneyStatus.UPLOADING
        val staged = try {
            temporaryFiles.stage(artifact, "evidence", time.now())
        } catch (error: Throwable) {
            status = journeyStatus(currentSession.state)
            throw mapFailure(error)
        }
        try {
            val body = temporaryFiles.data(staged)
            val digest = JourneyJson.sha256Hex(body)
            val key = idempotencyKey("upload.issue:$taskId", "capture_upload")
            val issued = client.createEvidenceUpload(
                CaptureEvidenceUploadCreate(
                    requirementKey = task.requirementKey,
                    artefact = task.artefact,
                    acquisitionMethod = method,
                    fallbackCondition = task.fallbackCondition?.wire,
                    expectedBytes = body.size,
                    expectedDigest = "sha256:$digest",
                    mediaType = artifact.contentType,
                    region = currentSession.region,
                ),
                key,
            )
            val etag = issued.etag ?: throw IdenqaException.InvalidResponse
            val accepted = client.uploadEvidence(issued.upload.id, artifact.contentType, body, etag, digest)
            temporaryFiles.remove(staged)
            if (accepted.upload.state == "rejected" || accepted.upload.state == "expired") {
                throw IdenqaException.StateConflict
            }
            if (!accepted.upload.isAccepted) {
                guidance = CaptureGuidance(
                    CaptureGuidanceCode.CONFIRMATION_PENDING,
                    guidanceMessage(CaptureGuidanceCode.CONFIRMATION_PENDING),
                    CaptureRecoveryAction.REFRESH,
                )
                status = journeyStatus(currentSession.state)
                return@withLock snapshot()
            }
            refreshProgress()
            guidance = if (plan?.currentTask?.id == taskId) {
                CaptureGuidance(
                    CaptureGuidanceCode.CONFIRMATION_PENDING,
                    guidanceMessage(CaptureGuidanceCode.CONFIRMATION_PENDING),
                    CaptureRecoveryAction.REFRESH,
                )
            } else {
                CaptureGuidance.None
            }
            status = journeyStatus(currentSession.state)
            snapshot()
        } catch (error: Throwable) {
            runCatching { temporaryFiles.remove(staged) }
            status = journeyStatus(currentSession.state)
            throw mapFailure(error)
        }
    }

    /** Records a failed live capture attempt and applies only policy-approved `capture_failed` fallbacks. */
    suspend fun captureFailed(taskId: String, reason: CaptureGuidanceCode = CaptureGuidanceCode.QUALITY_REJECTED): CaptureJourneySnapshot = mutex.withLock {
        val currentSession = session ?: throw IdenqaException.StateConflict
        val currentPlan = plan ?: throw IdenqaException.StateConflict
        val stored = reference ?: throw IdenqaException.StateConflict
        if (currentPlan.currentTask?.id != taskId) throw IdenqaException.StateConflict
        val failedTaskIds = (stored.failedTaskIds + taskId).distinct()
        reference = stored.copy(failedTaskIds = failedTaskIds)
        plan = CapturePlanBuilder.build(
            currentSession, captureCapabilities, configuration.preferredMethods, completions, failedTaskIds.toSet(),
        )
        referenceStore.write(reference!!)
        guidance = CaptureGuidance(reason, guidanceMessage(reason), CaptureRecoveryAction.RETRY)
        status = journeyStatus(currentSession.state)
        snapshot()
    }

    // MARK: - Cancellation

    /** Idempotent server-side cancellation. Retrying with the same key replays the original receipt. */
    suspend fun cancel(idempotencyKey: String? = null): CaptureCancellation = mutex.withLock {
        val stored = referenceStore.read() ?: reference ?: throw IdenqaException.NotStarted
        val key = idempotencyKey ?: stored.idempotencyKeys["cancel"] ?: idempotencyKeyFactory("capture_cancel")
        JourneyJson.structuredString(key)
        reference = stored.copy(idempotencyKeys = stored.idempotencyKeys + ("cancel" to key))
        referenceStore.write(reference!!)
        val expected = session?.version ?: stored.sessionVersion
        try {
            val receipt = client.cancel(expected, key)
            status = CaptureJourneyStatus.CANCELLED
            guidance = CaptureGuidance.None
            CaptureCancellation(receipt.eventId, receipt.verificationId, receipt.state, receipt.version, receipt.occurredAt)
        } catch (error: Throwable) {
            if (error == IdenqaException.StateConflict) {
                runCatching { client.getSession() }.getOrNull()?.let { detail ->
                    session = detail
                    if (isTerminal(detail.state)) status = journeyStatus(detail.state)
                }
            }
            throw mapFailure(error)
        }
    }

    // MARK: - Local data

    /** Removes every local credential, reference, cached progress value, and temporary file, then verifies. */
    suspend fun clearLocalData(): CaptureClearReport = mutex.withLock {
        val removed = runCatching { temporaryFiles.purgeAll() }.getOrDefault(0)
        tokenStore.clear()
        referenceStore.clear()
        proofKey.clear()
        session = null
        reference = null
        plan = null
        completions = emptyList()
        progressETag = null
        guidance = CaptureGuidance.None
        status = CaptureJourneyStatus.IDLE
        val tokenCleared = runCatching { tokenStore.read() }.getOrNull() == null
        val referencesCleared = runCatching { referenceStore.read() }.getOrNull() == null
        CaptureClearReport(
            tokenCleared = tokenCleared,
            referencesCleared = referencesCleared,
            temporaryFilesRemoved = removed,
            proofKeyCleared = proofKey.isCleared(),
            progressCacheCleared = progressETag == null && completions.isEmpty(),
        )
    }

    // MARK: - Snapshot and captureCapabilities

    suspend fun currentSnapshot(): CaptureJourneySnapshot = mutex.withLock { snapshot() }

    fun getCapabilities(): CaptureCapabilities = captureCapabilities

    // MARK: - Host lifecycle

    /** Backgrounding never discards the persisted reference or in-flight work. */
    suspend fun applicationEnteredBackground(): CaptureJourneySnapshot = mutex.withLock {
        if (canCancel()) {
            guidance = CaptureGuidance(
                CaptureGuidanceCode.BACKGROUNDED,
                guidanceMessage(CaptureGuidanceCode.BACKGROUNDED),
                CaptureRecoveryAction.WAIT,
            )
        }
        snapshot()
    }

    /** Foreground re-attachment is read-only and idempotent. */
    suspend fun applicationEnteredForeground(): CaptureJourneySnapshot {
        mutex.withLock {
            if (reference == null) return snapshot()
            if (guidance.code == CaptureGuidanceCode.BACKGROUNDED) guidance = CaptureGuidance.None
        }
        return refresh()
    }

    suspend fun captureInterruptionBegan(): CaptureJourneySnapshot = mutex.withLock {
        if (canCancel()) {
            guidance = CaptureGuidance(
                CaptureGuidanceCode.INTERRUPTED,
                guidanceMessage(CaptureGuidanceCode.INTERRUPTED),
                CaptureRecoveryAction.RETRY,
            )
        }
        snapshot()
    }

    suspend fun captureInterruptionEnded(): CaptureJourneySnapshot = mutex.withLock {
        if (guidance.code == CaptureGuidanceCode.INTERRUPTED) guidance = CaptureGuidance.None
        snapshot()
    }

    suspend fun cameraPermissionChanged(granted: Boolean): CaptureJourneySnapshot = mutex.withLock {
        if (granted) {
            if (guidance.code == CaptureGuidanceCode.CAMERA_PERMISSION_DENIED) guidance = CaptureGuidance.None
        } else {
            guidance = CaptureGuidance(
                CaptureGuidanceCode.CAMERA_PERMISSION_DENIED,
                guidanceMessage(CaptureGuidanceCode.CAMERA_PERMISSION_DENIED),
                CaptureRecoveryAction.OPEN_SETTINGS,
            )
        }
        snapshot()
    }

    suspend fun cameraBecameUnavailable(): CaptureJourneySnapshot = mutex.withLock {
        guidance = CaptureGuidance(
            CaptureGuidanceCode.CAMERA_UNAVAILABLE,
            guidanceMessage(CaptureGuidanceCode.CAMERA_UNAVAILABLE),
            CaptureRecoveryAction.RETRY,
        )
        snapshot()
    }

    suspend fun networkBecameUnavailable(): CaptureJourneySnapshot = mutex.withLock {
        guidance = CaptureGuidance(
            CaptureGuidanceCode.NETWORK_UNAVAILABLE,
            guidanceMessage(CaptureGuidanceCode.NETWORK_UNAVAILABLE),
            CaptureRecoveryAction.RETRY,
        )
        snapshot()
    }

    suspend fun networkBecameAvailable(): CaptureJourneySnapshot = mutex.withLock {
        if (guidance.code == CaptureGuidanceCode.NETWORK_UNAVAILABLE) guidance = CaptureGuidance.None
        snapshot()
    }

    /** Applies platform sensitive-screen protection while a capture preview is visible. */
    suspend fun applySensitiveScreenProtection(): Boolean {
        screenProtection.applyProtection()
        return screenProtection.isAvailable
    }

    suspend fun removeSensitiveScreenProtection() {
        screenProtection.removeProtection()
    }

    suspend fun screenCaptureState(): Boolean = screenProtection.isCaptured()

    suspend fun screenCaptureChanged(isCaptured: Boolean): CaptureJourneySnapshot = mutex.withLock {
        if (isCaptured) {
            guidance = CaptureGuidance(
                CaptureGuidanceCode.SECURE_SCREEN_CAPTURED,
                guidanceMessage(CaptureGuidanceCode.SECURE_SCREEN_CAPTURED),
                CaptureRecoveryAction.WAIT,
            )
        } else if (guidance.code == CaptureGuidanceCode.SECURE_SCREEN_CAPTURED) {
            guidance = CaptureGuidance.None
        }
        snapshot()
    }

    // MARK: - Internals

    private suspend fun attach(detail: CaptureSessionDetail) {
        configuration.captureProfileId?.let { if (it != detail.profileId) throw IdenqaException.StateConflict }
        configuration.region?.let { if (it != detail.region) throw IdenqaException.StateConflict }
        if (reference == null) reference = runCatching { referenceStore.read() }.getOrNull()
        val progressResult = client.getProgress()
        val progress = progressResult.progress?.takeIf { it.verificationId == detail.id }
            ?: throw IdenqaException.InvalidResponse
        completions = progress.completions
        progressETag = progressResult.etag
        val failed = reference?.failedTaskIds?.toSet() ?: emptySet()
        val rebuilt = CapturePlanBuilder.build(
            detail, captureCapabilities, configuration.preferredMethods, progress.completions, failed,
        )
        plan = rebuilt
        session = detail
        val updated = CaptureJourneyReference(
            verificationId = detail.id,
            sessionVersion = detail.version,
            region = detail.region,
            profileRevision = detail.profileRevision,
            profileDigest = detail.profileDigest,
            expiresAt = detail.expiresAt,
            completedTaskIds = rebuilt.completedTaskIds.sorted(),
            failedTaskIds = failed.sorted(),
            idempotencyKeys = reference?.idempotencyKeys ?: emptyMap(),
        )
        reference = updated
        referenceStore.write(updated)
        status = journeyStatus(detail.state)
    }

    private suspend fun refreshProgress() {
        val currentSession = session ?: return
        val result = client.getProgress(progressETag)
        if (result.notModified) return
        val progress = result.progress?.takeIf { it.verificationId == currentSession.id }
            ?: throw IdenqaException.InvalidResponse
        completions = progress.completions
        progressETag = result.etag
        val failed = reference?.failedTaskIds?.toSet() ?: emptySet()
        val rebuilt = CapturePlanBuilder.build(
            currentSession, captureCapabilities, configuration.preferredMethods, progress.completions, failed,
        )
        plan = rebuilt
        reference?.let {
            val updated = it.copy(
                completedTaskIds = rebuilt.completedTaskIds.sorted(),
                sessionVersion = currentSession.version,
            )
            reference = updated
            referenceStore.write(updated)
        }
    }

    private suspend fun idempotencyKey(storageKey: String, prefix: String): String {
        val stored = reference ?: throw IdenqaException.NotStarted
        stored.idempotencyKeys[storageKey]?.let {
            JourneyJson.structuredString(it)
            return it
        }
        val key = idempotencyKeyFactory(prefix)
        JourneyJson.structuredString(key)
        val updated = stored.copy(idempotencyKeys = stored.idempotencyKeys + (storageKey to key))
        reference = updated
        runCatching { referenceStore.write(updated) }
        return key
    }

    private fun mapFailure(error: Throwable): Throwable {
        if (error is IOException) {
            guidance = CaptureGuidance(
                CaptureGuidanceCode.NETWORK_UNAVAILABLE,
                guidanceMessage(CaptureGuidanceCode.NETWORK_UNAVAILABLE),
                CaptureRecoveryAction.RETRY,
            )
            return IdenqaException.NetworkUnavailable
        }
        when (error) {
            IdenqaException.NoCompatibleMethod -> guidance = CaptureGuidance(
                CaptureGuidanceCode.NO_COMPATIBLE_METHOD,
                guidanceMessage(CaptureGuidanceCode.NO_COMPATIBLE_METHOD),
                CaptureRecoveryAction.CHOOSE_ANOTHER_METHOD,
            )

            is IdenqaException.TemporaryStorage, is IdenqaException.Transport -> if (guidance.code == CaptureGuidanceCode.NONE) {
                guidance = CaptureGuidance(
                    CaptureGuidanceCode.UPLOAD_REJECTED,
                    guidanceMessage(CaptureGuidanceCode.UPLOAD_REJECTED),
                    CaptureRecoveryAction.RETRY,
                )
            }
        }
        return error
    }

    private fun snapshot(): CaptureJourneySnapshot = CaptureJourneySnapshot(
        status = status,
        verificationId = session?.id ?: reference?.verificationId,
        sessionVersion = session?.version ?: reference?.sessionVersion,
        region = session?.region ?: reference?.region,
        expiresAt = session?.expiresAt ?: reference?.expiresAt,
        currentTask = plan?.currentTask,
        completedTaskCount = plan?.completedTaskIds?.size ?: 0,
        remainingTaskCount = plan?.let { it.tasks.size - it.completedTaskIds.size } ?: 0,
        guidance = guidance,
    )

    private fun canCancel(): Boolean = when (status) {
        CaptureJourneyStatus.IDLE, CaptureJourneyStatus.COMPLETED, CaptureJourneyStatus.CANCELLED,
        CaptureJourneyStatus.EXPIRED, CaptureJourneyStatus.FAILED,
        -> false

        else -> reference != null
    }

    private fun isTerminal(state: String): Boolean = state in setOf("completed", "cancelled", "expired", "failed")

    private fun journeyStatus(state: String): CaptureJourneyStatus = when (state) {
        "created", "collecting" -> CaptureJourneyStatus.CAPTURE_REQUIRED
        "awaiting_input" -> CaptureJourneyStatus.AWAITING_INPUT
        "processing" -> CaptureJourneyStatus.PROCESSING
        "awaiting_external" -> CaptureJourneyStatus.AWAITING_EXTERNAL
        "manual_review" -> CaptureJourneyStatus.MANUAL_REVIEW
        "completed" -> CaptureJourneyStatus.COMPLETED
        "cancelled" -> CaptureJourneyStatus.CANCELLED
        "expired" -> CaptureJourneyStatus.EXPIRED
        "failed" -> CaptureJourneyStatus.FAILED
        else -> CaptureJourneyStatus.BLOCKED
    }

    internal fun guidanceMessage(code: CaptureGuidanceCode): String = when (code) {
        CaptureGuidanceCode.NONE -> ""
        CaptureGuidanceCode.CAMERA_PERMISSION_DENIED -> "Allow camera access to continue. You can enable it in Settings."
        CaptureGuidanceCode.CAMERA_UNAVAILABLE -> "The camera is not available right now. Try again or choose another option."
        CaptureGuidanceCode.NETWORK_UNAVAILABLE -> "Check your connection and try again. Your progress is saved."
        CaptureGuidanceCode.BACKGROUNDED -> "Your progress is saved. Return when you are ready to continue."
        CaptureGuidanceCode.INTERRUPTED -> "Capture was interrupted. Your progress is saved."
        CaptureGuidanceCode.QUALITY_REJECTED -> "That capture did not meet the required quality. Try again."
        CaptureGuidanceCode.CAPTURE_TIMEOUT -> "Capture took too long. Try again."
        CaptureGuidanceCode.SECURE_SCREEN_CAPTURED -> "Screen recording or mirroring is active. Stop it to protect your information."
        CaptureGuidanceCode.NO_COMPATIBLE_METHOD -> "No approved capture option is available on this device."
        CaptureGuidanceCode.UPLOAD_REJECTED -> "The upload was not accepted. Try again."
        CaptureGuidanceCode.CONFIRMATION_PENDING -> "Finishing this step. Refresh in a moment."
        CaptureGuidanceCode.PROFILE_MISMATCH -> "This capture link is no longer valid."
    }
}
