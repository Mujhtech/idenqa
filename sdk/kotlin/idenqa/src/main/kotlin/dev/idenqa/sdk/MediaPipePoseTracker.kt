package dev.idenqa.sdk

import android.content.Context
import com.google.mediapipe.framework.image.BitmapImageBuilder
import com.google.mediapipe.tasks.core.BaseOptions
import com.google.mediapipe.tasks.vision.core.RunningMode
import com.google.mediapipe.tasks.vision.facelandmarker.FaceLandmarker
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.security.MessageDigest
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.currentCoroutineContext
import kotlin.math.atan2
import kotlin.math.hypot

/** Local CPU adapter. The integrating application packages the pinned model in
 * its assets; there is no runtime model download. Close after every camera run. */
class MediaPipePoseTracker(context: Context, private val modelAssetPath: String = "idenqa/face_landmarker.task") : CapturePoseTracker, AutoCloseable {
    private val context = context.applicationContext
    private val lock = Any()
    private var tracker: FaceLandmarker? = null
    private var closed = false

    override suspend fun measure(artifact: CapturedArtifact): CaptureFacePose = withContext(Dispatchers.Default) {
        currentCoroutineContext().ensureActive()
        synchronized(lock) {
            if (closed) throw IdenqaException.CameraUnavailable
            val engine = tracker ?: create().also { tracker=it }
            val bitmap = decodeUprightCapture(artifact,1280)
            val image = BitmapImageBuilder(bitmap).build()
            try {
                val result=engine.detect(image)
                val face=result.faceLandmarks().firstOrNull()
                val m=result.facialTransformationMatrixes().orElse(emptyList()).firstOrNull()
                val blend=result.faceBlendshapes().orElse(emptyList()).firstOrNull()
                val xs=face?.map { it.x().toDouble() } ?: listOf(0.0)
                val ys=face?.map { it.y().toDouble() } ?: listOf(0.0)
                val degrees=180/Math.PI
                CaptureFacePose(result.faceLandmarks().size,
                    if(m!=null) -atan2(m[8].toDouble(),m[10].toDouble())*degrees else Double.NaN,
                    if(m!=null) atan2(-m[9].toDouble(),hypot(m[8].toDouble(),m[10].toDouble()))*degrees else Double.NaN,
                    if(m!=null) atan2(m[1].toDouble(),m[0].toDouble())*degrees else Double.NaN,
                    (xs.min()+xs.max())/2,(ys.min()+ys.max())/2,xs.max()-xs.min(),ys.max()-ys.min(),
                    blend?.firstOrNull { it.categoryName()=="eyeBlinkLeft" }?.score()?.toDouble() ?: Double.NaN,
                    blend?.firstOrNull { it.categoryName()=="eyeBlinkRight" }?.score()?.toDouble() ?: Double.NaN)
            } finally { image.close(); bitmap.recycle() }
        }
    }

    private fun create(): FaceLandmarker {
        val bytes=context.assets.open(modelAssetPath).use { input ->
            val output=java.io.ByteArrayOutputStream()
            val chunk=ByteArray(8192)
            while(output.size()<=8*1024*1024) {
                val count=input.read(chunk)
                if(count<0) break
                output.write(chunk,0,count)
            }
            output.toByteArray()
        }
        if(bytes.size>8*1024*1024 || MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) } != MODEL_SHA256) {
            throw IdenqaException.InvalidConfiguration
        }
        val buffer=ByteBuffer.allocateDirect(bytes.size).order(ByteOrder.nativeOrder()).put(bytes)
        buffer.rewind()
        return FaceLandmarker.createFromOptions(context,FaceLandmarker.FaceLandmarkerOptions.builder()
            .setBaseOptions(BaseOptions.builder().setModelAssetBuffer(buffer).build())
            .setRunningMode(RunningMode.IMAGE).setNumFaces(2)
            .setMinFaceDetectionConfidence(0.7f).setMinFacePresenceConfidence(0.7f).setMinTrackingConfidence(0.7f)
            .setOutputFaceBlendshapes(true).setOutputFacialTransformationMatrixes(true).build())
    }
    override fun close() { synchronized(lock) { closed=true; tracker?.close(); tracker=null } }
    companion object {
        const val MODEL_SHA256 = "64184e229b263107bc2b804c6625db1341ff2bb731874b0bcc2fe6544e0bc9ff"
    }
}
