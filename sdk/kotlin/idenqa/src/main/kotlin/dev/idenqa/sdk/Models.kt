package dev.idenqa.sdk

import java.time.Instant

sealed class IdenqaException(message: String) : Exception(message) {
    data object InvalidConfiguration : IdenqaException("invalid configuration")
    data object InvalidResponse : IdenqaException("invalid response")
    data class Transport(val status: Int) : IdenqaException("transport status $status")
    data object StateConflict : IdenqaException("capture state conflict")
    data object SecureStorage : IdenqaException("secure storage failure")
    data object HardwareSecurityUnavailable : IdenqaException("hardware-backed key unavailable")
    data object CameraUnavailable : IdenqaException("camera unavailable")
    data object CaptureQuality : IdenqaException("capture quality rejected")
    data object CaptureTimeout : IdenqaException("capture timed out")
    data object NotStarted : IdenqaException("capture journey has not been started")
    data object NotFound : IdenqaException("resource not found")
    data object Unauthenticated : IdenqaException("capture credential rejected")
    data object NetworkUnavailable : IdenqaException("network unavailable")
    data object TemporaryStorage : IdenqaException("temporary storage failure")
    data object NoCompatibleMethod : IdenqaException("no compatible capture method")
}

interface CaptureTokenStore {
    suspend fun read(): String?
    suspend fun write(token: String)
    suspend fun clear()
}

class CapturedArtifact(bytes: ByteArray, val contentType: String, val acquisitionMethod: String) {
    private val content = bytes.copyOf()
    fun bytes(): ByteArray = content.copyOf()

    /** Evidence bytes never appear in descriptions, logs, or crash reports. */
    override fun toString(): String =
        "CapturedArtifact(contentType=$contentType, acquisitionMethod=$acquisitionMethod, bytes=<redacted>)"
}

enum class CaptureCamera { FRONT, BACK }

enum class LivenessPrompt { NEUTRAL, TURN_LEFT, TURN_RIGHT, LOOK_UP, LOOK_DOWN, BLINK }

data class CaptureQualityPolicy(
    val minimumWidth: Int,
    val minimumHeight: Int,
    val maximumBytes: Int,
    val minimumBrightness: Double? = null,
    val maximumBrightness: Double? = null,
    val minimumContrast: Double? = null,
    val minimumSharpness: Double? = null,
    val maximumGlare: Double? = null,
    val requiredFaceCount: Int? = null,
)

data class CaptureQualityMeasurement(
    val width: Int,
    val height: Int,
    val byteCount: Int,
    val brightness: Double,
    val contrast: Double,
    val sharpness: Double,
    val glare: Double,
    val faceCount: Int,
) {
    fun failures(policy: CaptureQualityPolicy): List<String> = buildList {
        if (width <= 0 || height <= 0 || byteCount <= 0 || faceCount < 0 ||
            listOf(brightness,contrast,sharpness,glare).any { !it.isFinite() || it !in 0.0..1.0 }) {
            add("invalid_measurement")
            return@buildList
        }
        if (width < policy.minimumWidth || height < policy.minimumHeight) add("dimensions")
        if (byteCount > policy.maximumBytes) add("size")
        if (policy.minimumBrightness?.let { brightness < it } == true) add("too_dark")
        if (policy.maximumBrightness?.let { brightness > it } == true) add("too_bright")
        if (policy.minimumContrast?.let { contrast < it } == true) add("low_contrast")
        if (policy.minimumSharpness?.let { sharpness < it } == true) add("blur")
        if (policy.maximumGlare?.let { glare > it } == true) add("glare")
        if (policy.requiredFaceCount?.let { faceCount != it } == true) add("face_count")
    }
}

data class LivenessChallenge(val id: String, val prompt: LivenessPrompt, val maximumDurationMillis: Long, val pose: CapturePosePolicy? = null)

data class CaptureRequirement(
    val id: String,
    val evidenceType: String,
    val artefact: String,
    val camera: CaptureCamera,
    val quality: CaptureQualityPolicy,
    val challenges: List<LivenessChallenge> = emptyList(),
)

data class AcquiredFrame(
    val challengeId: String?,
    val artifact: CapturedArtifact,
    val quality: CaptureQualityMeasurement,
    val capturedAt: Instant = Instant.now(),
)

fun interface CaptureQualityAssessor {
    suspend fun assess(artifact: CapturedArtifact): CaptureQualityMeasurement
}

fun interface ChallengePresenter {
    suspend fun present(challenge: LivenessChallenge)
}

fun interface RawCaptureSource { suspend fun capture(): CapturedArtifact }

data class CapabilityAdvertisement(
    val sdkVersion: String,
    val implementedMethods: List<String>,
    val currentlyAvailableMethods: List<String>,
    val platform: String = "android",
)

data class CaptureSession(
    val id: String,
    val state: String,
    val version: Long,
    val region: String,
    val expiresAt: Instant,
)

data class RealtimeEvent(val sequence: Long, val type: String, val sessionVersion: Long)

class CaptureSessionState(initial: CaptureSession) {
    private val lock = Any()
    private var current = initial
    private var sequence = 0L

    fun snapshot(): CaptureSession = synchronized(lock) { current }

    fun apply(event: RealtimeEvent): CaptureSession = synchronized(lock) {
        if (event.sequence != sequence + 1 || event.sessionVersion < current.version) throw IdenqaException.StateConflict
        sequence = event.sequence
        current = current.copy(state = event.type, version = event.sessionVersion)
        current
    }
}
