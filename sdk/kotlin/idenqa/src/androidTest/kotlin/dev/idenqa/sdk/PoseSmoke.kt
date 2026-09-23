package dev.idenqa.sdk

import android.app.Activity
import android.app.Instrumentation
import android.graphics.Bitmap
import android.graphics.Color
import android.os.Bundle
import java.io.ByteArrayOutputStream
import kotlinx.coroutines.runBlocking

/** Actual JNI/model smoke evidence; not a camera, biometric or UI acceptance test. */
class PoseSmoke: Instrumentation() {
    override fun onCreate(arguments: Bundle?) { super.onCreate(arguments); start() }
    override fun onStart() {
        val status=Bundle().apply {
            putString("class", "dev.idenqa.sdk.PoseSmoke")
            putString("test", "blankFrameDoesNotPass")
            putInt("numtests",1); putInt("current",1)
        }
        sendStatus(1,status)
        try {
            val bitmap=Bitmap.createBitmap(640,480,Bitmap.Config.ARGB_8888).apply { eraseColor(Color.GRAY) }
            val encoded=ByteArrayOutputStream()
            bitmap.compress(Bitmap.CompressFormat.JPEG,90,encoded); bitmap.recycle()
            val tracker=MediaPipePoseTracker(targetContext)
            try {
                val pose=runBlocking { tracker.measure(CapturedArtifact(encoded.toByteArray(),"image/jpeg","idenqa.method.live_camera")) }
                check(pose.faceCount==0) { "Blank image unexpectedly contained a tracked face" }
                val gate=CapturePoseGate(LivenessChallenge("challenge.neutral",LivenessPrompt.NEUTRAL,5000))
                check(!gate.update(pose,0.0).complete)
            } finally { tracker.close() }
            sendStatus(0,status)
            finish(Activity.RESULT_OK,Bundle().apply { putString("stream","\nOK (1 test)\n") })
        } catch(error: Throwable) {
            status.putString("stack",error.javaClass.name+": "+error.message)
            sendStatus(-2,status)
            finish(Activity.RESULT_CANCELED,Bundle().apply { putString("stream","\nFAILURES!!!\nTests run: 1, Failures: 1\n") })
        }
    }
}
