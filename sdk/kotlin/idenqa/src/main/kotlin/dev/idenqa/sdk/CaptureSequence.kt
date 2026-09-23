package dev.idenqa.sdk

import java.time.Instant
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive

data class CaptureEvidenceSequence(val sequenceDigest: String, val index: Int, val count: Int,
    val challengeId: String, val capturedAt: Instant, val previousDigest: String? = null) {
    internal fun wire(): Map<String,Any> = linkedMapOf<String,Any>("sequence_digest" to sequenceDigest,
        "index" to index,"count" to count,"challenge_id" to challengeId,"captured_at" to capturedAt.toString()).apply {
        previousDigest?.let { put("previous_digest",it) }
    }
}

suspend fun CaptureJourney.submitSequence(taskId: String, requirement: CaptureRequirement, frames: List<AcquiredFrame>): CaptureJourneySnapshot {
    val snapshot=currentSnapshot()
    val task=snapshot.currentTask ?: throw IdenqaException.NotStarted
    if(task.id!=taskId || task.evidenceType!=requirement.evidenceType || task.artefact!=requirement.artefact ||
        frames.size !in 2..8 || frames.map { it.challengeId } != requirement.challenges.map { it.id }) throw IdenqaException.InvalidConfiguration
    val digests=frames.map { frame -> frame.artifact.bytes().let { bytes -> try { "sha256:"+JourneyJson.sha256Hex(bytes) } finally { bytes.fill(0) } } }
    val manifest=NoticeJson.encode(linkedMapOf("session_id" to (snapshot.verificationId ?: ""),"requirement_id" to requirement.id,
        "challenges" to requirement.challenges.map { it.id }, "digests" to digests))
    val digest="sha256:"+JourneyJson.sha256Hex(manifest)
    frames.forEachIndexed { index, frame ->
        currentCoroutineContext().ensureActive()
        if(frame.quality.failures(requirement.quality).isNotEmpty()) throw IdenqaException.CaptureQuality
        submit(taskId,frame.artifact,"idenqa.method.live_camera",CaptureEvidenceSequence(digest,index,frames.size,
            requirement.challenges[index].id,frame.capturedAt,if(index==0) null else digests[index-1]))
    }
    return refresh()
}
