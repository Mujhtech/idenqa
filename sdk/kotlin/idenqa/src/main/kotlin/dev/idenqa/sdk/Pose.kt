package dev.idenqa.sdk

import kotlin.math.abs

/** Subject-right/up positive degrees. Pose compliance does not establish PAD. */
data class CaptureFacePose(val faceCount: Int, val yaw: Double, val pitch: Double, val roll: Double,
    val centerX: Double, val centerY: Double, val width: Double, val height: Double,
    val leftEyeClosed: Double, val rightEyeClosed: Double)

data class CapturePosePolicy(val targetDegrees: Double = 20.0, val toleranceDegrees: Double = 7.0,
    val holdDurationMilliseconds: Double = 450.0) {
    init {
        if (!targetDegrees.isFinite() || !toleranceDegrees.isFinite() || !holdDurationMilliseconds.isFinite() ||
            listOf(targetDegrees,toleranceDegrees,holdDurationMilliseconds).any { it % 1.0 != 0.0 } ||
            targetDegrees !in 15.0..30.0 || toleranceDegrees !in 3.0..8.0 || holdDurationMilliseconds !in 300.0..1500.0) throw IdenqaException.InvalidConfiguration
    }
}
data class CapturePoseProgress(val fraction: Double, val feedback: String, val complete: Boolean)
fun interface CapturePoseTracker { suspend fun measure(artifact: CapturedArtifact): CaptureFacePose }

/** Fresh gate for every challenge, equivalent to the Web gate. Time alone never passes. */
class CapturePoseGate(challenge: LivenessChallenge) {
    private val prompt = challenge.prompt
    private val policy = challenge.pose ?: CapturePosePolicy()
    private var baseline: CaptureFacePose? = null
    private var since: Double? = null
    private var last = Double.NEGATIVE_INFINITY
    private var samples = 0
    private var blinkClosed = false
    private var blinkSamples = 0
    fun reset() { since=null; samples=0; blinkClosed=false; blinkSamples=0 }
    private fun reject(feedback: String): CapturePoseProgress { reset(); return CapturePoseProgress(0.0,feedback,false) }
    fun update(pose: CaptureFacePose, at: Double, quality: Boolean = true): CapturePoseProgress {
        if (!at.isFinite() || at <= last) return reject("find_face")
        if (at-last > 500) reset()
        last=at
        if (pose.faceCount < 1 || listOf(pose.yaw,pose.pitch,pose.roll,pose.centerX,pose.centerY,pose.width,pose.height,pose.leftEyeClosed,pose.rightEyeClosed).any { !it.isFinite() } ||
            listOf(pose.centerX,pose.centerY,pose.width,pose.height,pose.leftEyeClosed,pose.rightEyeClosed).any { it !in 0.0..1.0 }) {
            baseline=null; return reject("find_face")
        }
        if (pose.faceCount != 1) { baseline=null; return reject("one_face") }
        if (!quality) return reject("quality")
        if (pose.width < 0.22 || pose.height < 0.28) return reject("move_closer")
        if (pose.width > 0.8 || pose.height > 0.88) return reject("move_back")
        if (abs(pose.centerX-0.5)>0.18 || abs(pose.centerY-0.5)>0.2 || abs(pose.roll)>12) return reject("center_face")
        val neutral=abs(pose.yaw)<=10 && abs(pose.pitch)<=12
        val origin=baseline
        if (origin==null) {
            if (!neutral || pose.leftEyeClosed>0.35 || pose.rightEyeClosed>0.35) return reject("face_forward")
            val held=hold(at)
            if(prompt==LivenessPrompt.NEUTRAL) return CapturePoseProgress(held,"hold_still",held==1.0)
            if(held==1.0) { baseline=pose; reset() }
            return CapturePoseProgress(0.0,"face_forward",false)
        }
        val yaw=pose.yaw-origin.yaw; val pitch=pose.pitch-origin.pitch
        if(prompt==LivenessPrompt.BLINK) {
            if(!neutral) return reject("face_forward")
            if(pose.leftEyeClosed>=0.65 && pose.rightEyeClosed>=0.65) {
                blinkSamples++; blinkClosed=blinkClosed || blinkSamples>=2; since=null; samples=0
                return CapturePoseProgress(if(blinkClosed) 0.6 else 0.3,"open_eyes",false)
            }
            if(!blinkClosed || pose.leftEyeClosed>0.35 || pose.rightEyeClosed>0.35) return reject("follow_prompt")
            val held=hold(at)
            return CapturePoseProgress(0.6+0.4*held,"hold_still",held==1.0)
        }
        val angle=when(prompt) { LivenessPrompt.TURN_RIGHT -> yaw; LivenessPrompt.TURN_LEFT -> -yaw; LivenessPrompt.LOOK_UP -> pitch; else -> -pitch }
        val cross=if(prompt==LivenessPrompt.TURN_RIGHT || prompt==LivenessPrompt.TURN_LEFT) pitch else yaw
        if(abs(cross)>12 || pose.leftEyeClosed>0.5 || pose.rightEyeClosed>0.5) return reject("follow_prompt")
        if(abs(angle-policy.targetDegrees)>policy.toleranceDegrees) {
            reset()
            return CapturePoseProgress(if(angle>policy.targetDegrees+policy.toleranceDegrees) 0.0 else (angle/policy.targetDegrees*0.7).coerceIn(0.0,0.7),"follow_prompt",false)
        }
        val held=hold(at)
        return CapturePoseProgress(0.7+0.3*held,"hold_still",held==1.0)
    }
    private fun hold(at: Double): Double {
        if(since==null) since=at
        samples++
        return minOf((at-since!!)/policy.holdDurationMilliseconds,samples/4.0,1.0)
    }
}
