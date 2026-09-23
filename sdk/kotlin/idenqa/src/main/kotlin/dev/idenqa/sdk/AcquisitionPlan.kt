package dev.idenqa.sdk

import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive

/** Backend-issued acquisition policy, bound to one attached Core session. */
class CaptureAcquisitionPlan(val sessionId: String, requirements: List<CaptureRequirement>) {
    val requirements = requirements.toList()
    init {
        if (sessionId.isBlank() || requirements.size !in 1..16 ||
            requirements.map { it.id }.distinct().size != requirements.size ||
            requirements.map { it.evidenceType to it.artefact }.distinct().size != requirements.size) throw IdenqaException.InvalidConfiguration
        requirements.forEach { it.validate() }
    }
    fun requirement(sessionId: String, task: CaptureTask): CaptureRequirement {
        if (this.sessionId != sessionId || "idenqa.method.live_camera" !in task.methodOptions) throw IdenqaException.StateConflict
        return requirements.singleOrNull { it.evidenceType == task.evidenceType && it.artefact == task.artefact }
            ?: throw IdenqaException.InvalidConfiguration
    }
}

fun interface CaptureAcquisitionPlanProvider {
    suspend fun plan(sessionId: String): CaptureAcquisitionPlan
}

internal fun CaptureRequirement.validate() {
    val q = quality
    if (id.isBlank() || evidenceType.isBlank() || artefact.isBlank() ||
        q.minimumWidth !in 320..16384 || q.minimumHeight !in 320..16384 || q.maximumBytes !in 1024..20971520 ||
        challenges.size > 8 || challenges.map { it.id }.distinct().size != challenges.size ||
        listOfNotNull(q.minimumBrightness,q.maximumBrightness,q.minimumContrast,q.minimumSharpness,q.maximumGlare).any { !it.isFinite() || it !in 0.0..1.0 } ||
        (q.minimumBrightness ?: 0.0) > (q.maximumBrightness ?: 1.0) ||
        q.requiredFaceCount?.let { it !in 0..4 } == true ||
        challenges.any { it.id.isBlank() || it.maximumDurationMillis !in 500..15000 ||
            it.pose?.let { pose -> it.maximumDurationMillis < pose.holdDurationMilliseconds * 2 + 500 } == true }) throw IdenqaException.InvalidConfiguration
}

internal suspend fun validateCapturePhoto(artifact: CapturedArtifact, requirement: CaptureRequirement,
                                         assessor: CaptureQualityAssessor): CaptureQualityMeasurement {
    requirement.validate()
    val bytes = artifact.bytes()
    val valid = artifact.acquisitionMethod == "idenqa.method.live_camera" && artifact.contentType == "image/jpeg" &&
        bytes.isNotEmpty() && bytes.size <= requirement.quality.maximumBytes
    bytes.fill(0)
    if (!valid) throw IdenqaException.CaptureQuality
    val quality = assessor.assess(artifact)
    currentCoroutineContext().ensureActive()
    if (quality.failures(requirement.quality).isNotEmpty()) throw IdenqaException.CaptureQuality
    return quality
}
