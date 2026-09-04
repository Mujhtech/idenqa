package dev.idenqa.sdk

import android.annotation.SuppressLint
import android.content.Context
import android.graphics.ImageFormat
import android.hardware.camera2.CameraCaptureSession
import android.hardware.camera2.CameraCharacteristics
import android.hardware.camera2.CameraDevice
import android.hardware.camera2.CameraManager
import android.media.ImageReader
import android.os.Handler
import android.os.HandlerThread
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import kotlinx.coroutines.suspendCancellableCoroutine

class Camera2PhotoCaptureSource(
    context: Context,
    private val lensFacing: Int = CameraCharacteristics.LENS_FACING_BACK,
) : RawCaptureSource {
    private val manager = context.applicationContext.getSystemService(CameraManager::class.java)

    @SuppressLint("MissingPermission")
    override suspend fun capture(): CapturedArtifact = suspendCancellableCoroutine { continuation ->
        val thread = HandlerThread("idenqa-camera-capture").apply { start() }
        val handler = Handler(thread.looper)
        val closed = AtomicBoolean(false)
        var camera: CameraDevice? = null
        var session: CameraCaptureSession? = null
        val reader = ImageReader.newInstance(1920, 1080, ImageFormat.JPEG, 1)
        fun close() {
            if (closed.compareAndSet(false, true)) {
                session?.close()
                camera?.close()
                reader.close()
                thread.quitSafely()
            }
        }
        continuation.invokeOnCancellation { close() }
        try {
            val cameraId = manager.cameraIdList.firstOrNull {
                manager.getCameraCharacteristics(it).get(CameraCharacteristics.LENS_FACING) == lensFacing
            } ?: throw IdenqaException.CameraUnavailable
            reader.setOnImageAvailableListener({ source ->
                source.acquireLatestImage()?.use { image ->
                    val buffer = image.planes[0].buffer
                    val bytes = ByteArray(buffer.remaining()).also(buffer::get)
                    if (continuation.isActive) continuation.resume(CapturedArtifact(bytes, "image/jpeg", "idenqa.method.live_camera"))
                    bytes.fill(0)
                }
                close()
            }, handler)
            manager.openCamera(cameraId, object : CameraDevice.StateCallback() {
                override fun onOpened(device: CameraDevice) {
                    camera = device
                    device.createCaptureSession(listOf(reader.surface), object : CameraCaptureSession.StateCallback() {
                        override fun onConfigured(value: CameraCaptureSession) {
                            session = value
                            val request = device.createCaptureRequest(CameraDevice.TEMPLATE_STILL_CAPTURE).apply { addTarget(reader.surface) }.build()
                            value.capture(request, null, handler)
                        }
                        override fun onConfigureFailed(value: CameraCaptureSession) = fail(IdenqaException.CameraUnavailable)
                    }, handler)
                }
                override fun onDisconnected(device: CameraDevice) = fail(IdenqaException.CameraUnavailable)
                override fun onError(device: CameraDevice, error: Int) = fail(IdenqaException.CameraUnavailable)
                private fun fail(error: Throwable) {
                    if (continuation.isActive) continuation.resumeWithException(error)
                    close()
                }
            }, handler)
        } catch (error: Throwable) {
            if (continuation.isActive) continuation.resumeWithException(error)
            close()
        }
    }
}
