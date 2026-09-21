package dev.idenqa.sdk

import com.squareup.moshi.Moshi
import java.security.MessageDigest
import java.time.Instant
import java.util.Base64

// MARK: - Wire documents

internal data class CaptureProfileRegistry(val schemaVersion: Int, val revision: Int, val digest: String)

internal data class CaptureAcquisition(val strategy: String, val methods: List<String>)

internal data class CaptureFallback(val on: List<String>, val acquisition: CaptureAcquisition)

internal data class CaptureRequirementDocument(
    val key: String,
    val purpose: String,
    val evidenceType: String,
    val artefacts: List<String>,
    val acquisition: CaptureAcquisition,
    val requiredAssurances: List<String>,
    val fallbacks: List<CaptureFallback>,
)

internal data class CaptureProfileDocument(
    val schemaVersion: Int,
    val registry: CaptureProfileRegistry,
    val requirements: List<CaptureRequirementDocument>,
)

internal data class CaptureSessionDetail(
    val id: String,
    val state: String,
    val version: Long,
    val profileId: String,
    val profileRevision: Int,
    val profileDigest: String,
    val region: String,
    val requirements: CaptureProfileDocument,
    val expiresAt: Instant,
)

internal data class CaptureCompletion(
    val uploadId: String,
    val evidenceId: String,
    val requirementKey: String,
    val evidenceType: String,
    val artefact: String,
    val acquisitionMethod: String,
    val fallbackCondition: String?,
)

internal data class CaptureProgress(val verificationId: String, val completions: List<CaptureCompletion>)

internal data class CaptureCancellationDocument(
    val eventId: String,
    val verificationId: String,
    val state: String,
    val version: Long,
    val occurredAt: Instant,
)

internal data class EvidenceUpload(
    val id: String,
    val evidenceId: String,
    val state: String,
    val version: Long,
    val requirementKey: String,
    val artefact: String,
    val acquisitionMethod: String,
    val expectedBytes: Long,
    val mediaType: String,
    val region: String,
) {
    val isAccepted: Boolean get() = state == "accepted"
}

internal data class CaptureEvidenceUploadCreate(
    val requirementKey: String,
    val artefact: String,
    val acquisitionMethod: String,
    val fallbackCondition: String?,
    val expectedBytes: Int,
    val expectedDigest: String,
    val mediaType: String,
    val region: String,
)

internal data class CaptureProgressResult(
    val progress: CaptureProgress?,
    val etag: String?,
    val notModified: Boolean,
)

internal data class EvidenceUploadResult(val upload: EvidenceUpload, val etag: String?)

// MARK: - Public plan and task values

/** A policy-approved fallback reason. Unknown values are never invented locally. */
enum class CaptureFallbackCondition(val wire: String) {
    CAPABILITY_UNAVAILABLE("capability_unavailable"),
    METHOD_UNAVAILABLE("method_unavailable"),
    CAPTURE_FAILED("capture_failed");

    companion object {
        fun fromWire(value: String?): CaptureFallbackCondition? =
            entries.firstOrNull { it.wire == value }
    }
}

/** One capture step. Exactly one task is current at a time. */
data class CaptureTask(
    val id: String,
    val requirementKey: String,
    val evidenceType: String,
    val artefact: String,
    val methodOptions: List<String>,
    val fallbackCondition: CaptureFallbackCondition?,
    val requiredAssurances: List<String> = emptyList(),
)

/** Ordered plan derived from the immutable session snapshot and this device's capabilities. */
data class CapturePlan(
    val verificationId: String,
    val profileRevision: Int,
    val profileDigest: String,
    val tasks: List<CaptureTask>,
    val completedTaskIds: Set<String>,
) {
    val pendingTasks: List<CaptureTask> get() = tasks.filter { it.id !in completedTaskIds }
    val currentTask: CaptureTask? get() = pendingTasks.firstOrNull()
}

// MARK: - Journey values

enum class CaptureJourneyStatus {
    IDLE,
    STARTING,
    CAPTURE_REQUIRED,
    UPLOADING,
    AWAITING_INPUT,
    PROCESSING,
    AWAITING_EXTERNAL,
    MANUAL_REVIEW,
    COMPLETED,
    CANCELLED,
    EXPIRED,
    FAILED,
    BLOCKED,
}

enum class CaptureGuidanceCode {
    NONE,
    CAMERA_PERMISSION_DENIED,
    CAMERA_UNAVAILABLE,
    NETWORK_UNAVAILABLE,
    BACKGROUNDED,
    INTERRUPTED,
    QUALITY_REJECTED,
    CAPTURE_TIMEOUT,
    SECURE_SCREEN_CAPTURED,
    NO_COMPATIBLE_METHOD,
    UPLOAD_REJECTED,
    CONFIRMATION_PENDING,
    PROFILE_MISMATCH,
}

enum class CaptureRecoveryAction { NONE, RETRY, OPEN_SETTINGS, CHOOSE_ANOTHER_METHOD, WAIT, REFRESH }

/** Subject-safe guidance for the host UI. Cancellation stays reachable in every non-terminal state. */
data class CaptureGuidance(
    val code: CaptureGuidanceCode,
    val message: String,
    val recovery: CaptureRecoveryAction,
    val cancelAvailable: Boolean = true,
) {
    companion object {
        val None = CaptureGuidance(CaptureGuidanceCode.NONE, "", CaptureRecoveryAction.NONE)
    }
}

/** One screen at a time: hosts render `currentTask` and never the full plan. */
data class CaptureJourneySnapshot(
    val status: CaptureJourneyStatus,
    val verificationId: String?,
    val sessionVersion: Long?,
    val region: String?,
    val expiresAt: Instant?,
    val currentTask: CaptureTask?,
    val completedTaskCount: Int,
    val remainingTaskCount: Int,
    val guidance: CaptureGuidance,
) {
    val canCancel: Boolean
        get() = when (status) {
            CaptureJourneyStatus.IDLE, CaptureJourneyStatus.COMPLETED, CaptureJourneyStatus.CANCELLED,
            CaptureJourneyStatus.EXPIRED, CaptureJourneyStatus.FAILED,
            -> false

            else -> guidance.cancelAvailable
        }

    companion object {
        val Idle = CaptureJourneySnapshot(
            CaptureJourneyStatus.IDLE, null, null, null, null, null, 0, 0, CaptureGuidance.None,
        )
    }
}

/**
 * The only durable local journey state. It contains opaque references and no
 * evidence bytes, notice copy, subject data, or credentials other than the
 * separately secure-stored capture token.
 */
data class CaptureJourneyReference(
    val verificationId: String,
    val sessionVersion: Long,
    val region: String,
    val profileRevision: Int,
    val profileDigest: String,
    val expiresAt: Instant,
    val completedTaskIds: List<String> = emptyList(),
    val failedTaskIds: List<String> = emptyList(),
    val idempotencyKeys: Map<String, String> = emptyMap(),
)

/** Secure persistence for the minimum journey reference. */
interface CaptureJourneyStore {
    suspend fun read(): CaptureJourneyReference?
    suspend fun write(reference: CaptureJourneyReference)
    suspend fun clear()
}

/** Immutable receipt for committed subject cancellation. */
data class CaptureCancellation(
    val eventId: String,
    val verificationId: String,
    val state: String,
    val version: Long,
    val occurredAt: Instant,
)

data class CaptureClearReport(
    val tokenCleared: Boolean,
    val referencesCleared: Boolean,
    val temporaryFilesRemoved: Int,
    val proofKeyCleared: Boolean,
    val progressCacheCleared: Boolean,
) {
    val allCleared: Boolean get() = tokenCleared && referencesCleared && proofKeyCleared && progressCacheCleared
}

fun interface CaptureTimeSource {
    fun now(): Instant
}

object SystemCaptureTimeSource : CaptureTimeSource {
    override fun now(): Instant = Instant.now()
}

// MARK: - JSON mapping

internal object JourneyJson {
    private val adapter = Moshi.Builder().build().adapter(Any::class.java)

    fun decodeSession(encoded: String): CaptureSessionDetail = try {
        val value = objectValue(encoded)
        val requirements = objectValue(adapter.toJson(map(value, "requirements")))
        CaptureSessionDetail(
            id = string(value, "id"),
            state = string(value, "state"),
            version = number(value, "version"),
            profileId = string(value, "profile_id"),
            profileRevision = number(value, "profile_revision").toInt(),
            profileDigest = string(value, "profile_digest"),
            region = string(value, "region"),
            requirements = CaptureProfileDocument(
                schemaVersion = number(requirements, "schema_version").toInt(),
                registry = objectValue(adapter.toJson(map(requirements, "registry"))).let {
                    CaptureProfileRegistry(number(it, "schema_version").toInt(), number(it, "revision").toInt(), string(it, "digest"))
                },
                requirements = array(requirements, "requirements").map { parseRequirement(it) },
            ),
            expiresAt = Instant.parse(string(value, "expires_at")),
        )
    } catch (_: Exception) {
        throw IdenqaException.InvalidResponse
    }

    fun decodeProgress(encoded: String): CaptureProgress = try {
        val value = objectValue(encoded)
        CaptureProgress(
            verificationId = string(value, "verification_id"),
            completions = array(value, "completions").map { item ->
                val entry = map(item)
                CaptureCompletion(
                    uploadId = string(entry, "upload_id"),
                    evidenceId = string(entry, "evidence_id"),
                    requirementKey = string(entry, "requirement_key"),
                    evidenceType = string(entry, "evidence_type"),
                    artefact = string(entry, "artefact"),
                    acquisitionMethod = string(entry, "acquisition_method"),
                    fallbackCondition = entry["fallback_condition"] as? String,
                )
            },
        )
    } catch (_: Exception) {
        throw IdenqaException.InvalidResponse
    }

    fun decodeCancellation(encoded: String): CaptureCancellationDocument = try {
        val value = objectValue(encoded)
        CaptureCancellationDocument(
            eventId = string(value, "event_id"),
            verificationId = string(value, "verification_id"),
            state = string(value, "state"),
            version = number(value, "version"),
            occurredAt = Instant.parse(string(value, "occurred_at")),
        )
    } catch (_: Exception) {
        throw IdenqaException.InvalidResponse
    }

    fun decodeEvidenceUpload(encoded: String): EvidenceUpload = try {
        val value = objectValue(encoded)
        EvidenceUpload(
            id = string(value, "id"),
            evidenceId = string(value, "evidence_id"),
            state = string(value, "state"),
            version = number(value, "version"),
            requirementKey = string(value, "requirement_key"),
            artefact = string(value, "artefact"),
            acquisitionMethod = string(value, "acquisition_method"),
            expectedBytes = number(value, "expected_bytes"),
            mediaType = string(value, "media_type"),
            region = string(value, "region"),
        )
    } catch (_: Exception) {
        throw IdenqaException.InvalidResponse
    }

    fun encodeEvidenceUploadCreate(input: CaptureEvidenceUploadCreate): String = adapter.toJson(
        linkedMapOf(
            "requirement_key" to input.requirementKey,
            "artefact" to input.artefact,
            "acquisition_method" to input.acquisitionMethod,
            "fallback_condition" to input.fallbackCondition,
            "expected_bytes" to input.expectedBytes,
            "expected_digest" to input.expectedDigest,
            "media_type" to input.mediaType,
            "region" to input.region,
        ).filterValues { it != null },
    )

    fun encodeCancelCommand(expectedVersion: Long): String = adapter.toJson(mapOf("expected_version" to expectedVersion))

    fun encodeJourneyReference(reference: CaptureJourneyReference): String = adapter.toJson(
        linkedMapOf(
            "verification_id" to reference.verificationId,
            "session_version" to reference.sessionVersion,
            "region" to reference.region,
            "profile_revision" to reference.profileRevision,
            "profile_digest" to reference.profileDigest,
            "expires_at" to reference.expiresAt.toString(),
            "completed_task_ids" to reference.completedTaskIds,
            "failed_task_ids" to reference.failedTaskIds,
            "idempotency_keys" to reference.idempotencyKeys,
        ),
    )

    fun decodeJourneyReference(encoded: String): CaptureJourneyReference = try {
        val value = objectValue(encoded)
        CaptureJourneyReference(
            verificationId = string(value, "verification_id"),
            sessionVersion = number(value, "session_version"),
            region = string(value, "region"),
            profileRevision = number(value, "profile_revision").toInt(),
            profileDigest = string(value, "profile_digest"),
            expiresAt = Instant.parse(string(value, "expires_at")),
            completedTaskIds = stringList(value, "completed_task_ids"),
            failedTaskIds = stringList(value, "failed_task_ids"),
            idempotencyKeys = map(value, "idempotency_keys").entries.associate { (key, entryValue) ->
                (key as? String ?: throw IdenqaException.InvalidResponse) to
                    (entryValue as? String ?: throw IdenqaException.InvalidResponse)
            },
        )
    } catch (error: IdenqaException) {
        throw error
    } catch (_: Exception) {
        throw IdenqaException.InvalidResponse
    }

    /** RFC 9651 String encoding for the Idempotency-Key header field value. */
    fun structuredString(value: String): String {
        if (value.isEmpty() || value.any { it.code < 0x20 || it.code > 0x7e }) throw IdenqaException.InvalidConfiguration
        val encoded = "\"" + value.replace("\\", "\\\\").replace("\"", "\\\"") + "\""
        if (encoded.length > 130) throw IdenqaException.InvalidConfiguration
        return encoded
    }

    fun sha256Hex(value: ByteArray): String =
        MessageDigest.getInstance("SHA-256").digest(value).joinToString("") { "%02x".format(it) }

    fun contentDigestHeader(hex: String): String {
        val bytes = ByteArray(hex.length / 2) { index ->
            hex.substring(index * 2, index * 2 + 2).toInt(16).toByte()
        }
        return "sha-256=:${Base64.getEncoder().encodeToString(bytes)}:"
    }

    private fun parseRequirement(value: Any?): CaptureRequirementDocument {
        val entry = map(value)
        return CaptureRequirementDocument(
            key = string(entry, "key"),
            purpose = string(entry, "purpose"),
            evidenceType = string(entry, "evidence_type"),
            artefacts = stringList(entry, "artefacts"),
            acquisition = parseAcquisition(map(entry, "acquisition")),
            requiredAssurances = stringList(entry, "required_assurances"),
            fallbacks = array(entry, "fallbacks").map { item ->
                val fallback = map(item)
                CaptureFallback(stringList(fallback, "on"), parseAcquisition(map(fallback, "acquisition")))
            },
        )
    }

    private fun parseAcquisition(value: Map<*, *>): CaptureAcquisition =
        CaptureAcquisition(string(value, "strategy"), stringList(value, "methods"))

    private fun objectValue(encoded: String): Map<*, *> =
        adapter.fromJson(encoded) as? Map<*, *> ?: throw IdenqaException.InvalidResponse

    private fun map(value: Any?): Map<*, *> = value as? Map<*, *> ?: throw IdenqaException.InvalidResponse

    private fun map(value: Map<*, *>, name: String): Map<*, *> = value[name] as? Map<*, *> ?: throw IdenqaException.InvalidResponse

    private fun array(value: Map<*, *>, name: String): List<*> = value[name] as? List<*> ?: throw IdenqaException.InvalidResponse

    private fun string(value: Map<*, *>, name: String): String = value[name] as? String ?: throw IdenqaException.InvalidResponse

    private fun number(value: Map<*, *>, name: String): Long = (value[name] as? Number)?.toLong() ?: throw IdenqaException.InvalidResponse

    private fun stringList(value: Map<*, *>, name: String): List<String> =
        array(value, name).map { it as? String ?: throw IdenqaException.InvalidResponse }
}
