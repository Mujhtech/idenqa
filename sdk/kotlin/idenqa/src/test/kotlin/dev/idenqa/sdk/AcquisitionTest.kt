package dev.idenqa.sdk

import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.delay
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
class AcquisitionTest {
    @Test fun challengedAcquisitionCannotFallBackToTimedPhoto() = runTest {
        var captured = false
        val coordinator = AcquisitionCoordinator(
            RawCaptureSource { captured = true; CapturedArtifact(byteArrayOf(1),"image/jpeg","idenqa.method.live_camera") },
            CaptureQualityAssessor { CaptureQualityMeasurement(720,720,1,0.5,0.5,0.5,0.0,1) }, ChallengePresenter {})
        assertFailsWith<IdenqaException.NoCompatibleMethod> {
            coordinator.acquire(CaptureRequirement("selfie", "idenqa.evidence.selfie_image", "idenqa.artefact.selfie_image",
                CaptureCamera.FRONT,CaptureQualityPolicy(720,720,1024),listOf(LivenessChallenge("left",LivenessPrompt.TURN_LEFT,5000))))
        }
        kotlin.test.assertFalse(captured)
    }
    @Test fun preservesOrderedChallengeTranscriptWithoutClaimingLiveness() = runTest {
        var count = 0
        val presented = mutableListOf<String>()
        var prompt = LivenessPrompt.NEUTRAL
        var samples = 0
        val coordinator = AcquisitionCoordinator(
            RawCaptureSource { CapturedArtifact(byteArrayOf((++count).toByte()), "image/jpeg", "idenqa.method.live_camera") },
            CaptureQualityAssessor { artifact -> CaptureQualityMeasurement(1280, 720, artifact.bytes().size, 0.5, 0.4, 0.3, 0.01, 1) },
            ChallengePresenter { presented += it.id; prompt = it.prompt; samples = 0 },
            tracker = CapturePoseTracker {
                samples++
                val yaw = if(samples <= 8) 0.0 else when(prompt) { LivenessPrompt.TURN_LEFT -> -20.0; LivenessPrompt.TURN_RIGHT -> 20.0; else -> 0.0 }
                CaptureFacePose(1,yaw,0.0,0.0,0.5,0.5,0.4,0.5,0.0,0.0)
            },
            monotonicMilliseconds = { testScheduler.currentTime.toDouble() },
        )
        val frames = coordinator.acquire(
            CaptureRequirement(
                "selfie.live",
                "idenqa.evidence.selfie_image",
                "idenqa.artefact.selfie_image",
                CaptureCamera.FRONT,
                CaptureQualityPolicy(720, 720, 1024, requiredFaceCount = 1),
                listOf(
                    LivenessChallenge("neutral", LivenessPrompt.NEUTRAL, 5_000),
                    LivenessChallenge("left", LivenessPrompt.TURN_LEFT, 5_000),
                    LivenessChallenge("right", LivenessPrompt.TURN_RIGHT, 5_000),
                ),
            ),
        )
        assertEquals(listOf("neutral", "left", "right"), frames.map { it.challengeId })
        assertEquals(listOf("neutral", "left", "right"), presented)
    }

    @Test fun rejectsQualityBeforeReturningRawFrames() = runTest {
        val coordinator = AcquisitionCoordinator(
            RawCaptureSource { CapturedArtifact(byteArrayOf(1), "image/jpeg", "idenqa.method.live_camera") },
            CaptureQualityAssessor { CaptureQualityMeasurement(1280, 720, 1, 0.5, 0.4, 0.3, 0.01, 0) },
            ChallengePresenter {},
        )
        assertFailsWith<IdenqaException.CaptureQuality> {
            coordinator.acquire(
                CaptureRequirement(
                    "document.front",
                    "idenqa.evidence.document_image",
                    "idenqa.artefact.document_front",
                    CaptureCamera.BACK,
                    CaptureQualityPolicy(2000, 2000, 1024),
                ),
            )
        }
    }

    @Test fun enforcesServerChallengeDeadline() = runTest {
        val coordinator = AcquisitionCoordinator(
            RawCaptureSource {
                delay(60_000)
                CapturedArtifact(byteArrayOf(1), "image/jpeg", "idenqa.method.live_camera")
            },
            CaptureQualityAssessor { CaptureQualityMeasurement(1280, 720, 1, 0.5, 0.4, 0.3, 0.01, 1) },
            ChallengePresenter {},
            tracker = CapturePoseTracker { CaptureFacePose(0,0.0,0.0,0.0,0.5,0.5,0.4,0.5,0.0,0.0) },
        )
        assertFailsWith<IdenqaException.CaptureTimeout> {
            coordinator.acquire(
                CaptureRequirement(
                    "selfie.live",
                    "idenqa.evidence.selfie_image",
                    "idenqa.artefact.selfie_image",
                    CaptureCamera.FRONT,
                    CaptureQualityPolicy(720, 720, 1024),
                    listOf(LivenessChallenge("neutral", LivenessPrompt.NEUTRAL, 500)),
                ),
            )
        }
    }
}
