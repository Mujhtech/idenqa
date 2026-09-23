package dev.idenqa.sdk

import android.annotation.SuppressLint
import android.content.Context
import android.graphics.ImageFormat
import android.graphics.SurfaceTexture
import android.hardware.camera2.*
import android.media.ImageReader
import android.os.Handler
import android.os.HandlerThread
import android.view.Surface
import android.view.TextureView
import java.util.concurrent.atomic.AtomicBoolean

/** SDK-owned preview. Bytes never leave the native acquisition boundary. */
internal class CaptureCameraView(
    context: Context,
    private val front: Boolean,
    private val ready: () -> Unit,
    private val photo: (CapturedArtifact) -> Unit,
    private val failure: () -> Unit,
) : TextureView(context), TextureView.SurfaceTextureListener {
    private val thread = HandlerThread("idenqa-preview").apply { start() }
    private val handler = Handler(thread.looper)
    private val stopped = AtomicBoolean(false)
    private var camera: CameraDevice? = null
    private var session: CameraCaptureSession? = null
    private var reader: ImageReader? = null
    private var preview: Surface? = null
    private var sensorOrientation = 0
    private var capturing = false
    private var previewWidth = 0
    private var previewHeight = 0
    private val captureTimeout = Runnable { failed() }

    init { surfaceTextureListener = this; contentDescription = "Live camera preview" }

    @SuppressLint("MissingPermission")
    override fun onSurfaceTextureAvailable(texture: SurfaceTexture, width: Int, height: Int) {
        try {
            val manager = context.getSystemService(CameraManager::class.java)
            val facing = if(front) CameraCharacteristics.LENS_FACING_FRONT else CameraCharacteristics.LENS_FACING_BACK
            val id = manager.cameraIdList.firstOrNull { manager.getCameraCharacteristics(it).get(CameraCharacteristics.LENS_FACING) == facing }
                ?: throw IdenqaException.CameraUnavailable
            val characteristics = manager.getCameraCharacteristics(id)
            sensorOrientation = characteristics.get(CameraCharacteristics.SENSOR_ORIENTATION) ?: 0
            val sizes = characteristics.get(CameraCharacteristics.SCALER_STREAM_CONFIGURATION_MAP) ?: throw IdenqaException.CameraUnavailable
            val jpegSize = sizes.getOutputSizes(ImageFormat.JPEG).filter { it.width.toLong()*it.height <= 4_000_000 }.maxByOrNull { it.width.toLong()*it.height }
                ?: throw IdenqaException.CameraUnavailable
            val previewSize = sizes.getOutputSizes(SurfaceTexture::class.java).filter { it.width <= 1920 }.maxByOrNull { it.width.toLong()*it.height }
                ?: throw IdenqaException.CameraUnavailable
            texture.setDefaultBufferSize(previewSize.width,previewSize.height)
            previewWidth=previewSize.width; previewHeight=previewSize.height
            updateTransform(width,height)
            preview = Surface(texture)
            reader = ImageReader.newInstance(jpegSize.width,jpegSize.height,ImageFormat.JPEG,2).also { output ->
                output.setOnImageAvailableListener({ source ->
                    if (!stopped.get()) source.acquireLatestImage()?.use { image ->
                        val bytes = ByteArray(image.planes[0].buffer.remaining()).also { image.planes[0].buffer.get(it) }
                        val artifact = CapturedArtifact(bytes,"image/jpeg","idenqa.method.live_camera")
                        bytes.fill(0)
                        post { if (!stopped.get()) { capturing=false; removeCallbacks(captureTimeout); photo(artifact) } }
                    }
                },handler)
            }
            manager.openCamera(id,object: CameraDevice.StateCallback() {
                override fun onOpened(device: CameraDevice) {
                    if (stopped.get()) { device.close(); return }
                    camera = device
                    device.createCaptureSession(listOf(preview!!,reader!!.surface),object: CameraCaptureSession.StateCallback() {
                        override fun onConfigured(value: CameraCaptureSession) {
                            if (stopped.get()) { value.close(); return }
                            session = value
                            try {
                                val request = device.createCaptureRequest(CameraDevice.TEMPLATE_PREVIEW).apply {
                                    addTarget(preview!!)
                                    set(CaptureRequest.CONTROL_AF_MODE,CaptureRequest.CONTROL_AF_MODE_CONTINUOUS_PICTURE)
                                }.build()
                                value.setRepeatingRequest(request,null,handler)
                                post { if (!stopped.get()) ready() }
                            } catch (_: Exception) { failed() }
                        }
                        override fun onConfigureFailed(value: CameraCaptureSession) { value.close(); failed() }
                    },handler)
                }
                override fun onDisconnected(device: CameraDevice) { device.close(); failed() }
                override fun onError(device: CameraDevice,error: Int) { device.close(); failed() }
            },handler)
        } catch (_: Exception) { failed() }
    }

    fun takePhoto() {
        if (capturing || stopped.get()) return
        capturing = true
        postDelayed(captureTimeout,15_000)
        val rotation = when(display?.rotation) { Surface.ROTATION_90 -> 90; Surface.ROTATION_180 -> 180; Surface.ROTATION_270 -> 270; else -> 0 }
        handler.post {
            if (stopped.get()) return@post
            try {
                val request = camera!!.createCaptureRequest(CameraDevice.TEMPLATE_STILL_CAPTURE).apply {
                    addTarget(reader!!.surface)
                    set(CaptureRequest.JPEG_ORIENTATION,(sensorOrientation + (if(front) rotation else -rotation) + 360) % 360)
                    set(CaptureRequest.CONTROL_AF_MODE,CaptureRequest.CONTROL_AF_MODE_CONTINUOUS_PICTURE)
                }.build()
                session!!.capture(request,object: CameraCaptureSession.CaptureCallback() {
                    override fun onCaptureFailed(session: CameraCaptureSession,request: CaptureRequest,failure: CaptureFailure) { failed() }
                },handler)
            } catch (_: Exception) { failed() }
        }
    }

    private fun failed() { post { if (!stopped.get()) { close(); failure() } } }
    fun close() {
        if (!stopped.compareAndSet(false,true)) return
        removeCallbacks(captureTimeout)
        handler.post {
            session?.close(); camera?.close(); reader?.close(); preview?.release()
            session=null; camera=null; reader=null; preview=null
            thread.quitSafely()
        }
    }
    override fun onSurfaceTextureDestroyed(surface: SurfaceTexture): Boolean { close(); return true }
    override fun onSurfaceTextureSizeChanged(surface: SurfaceTexture,width: Int,height: Int) { updateTransform(width,height) }
    private fun updateTransform(width: Int, height: Int) {
        if (width<=0 || height<=0 || previewWidth<=0 || previewHeight<=0) return
        val rotation=when(display?.rotation) { Surface.ROTATION_90 -> 90; Surface.ROTATION_180 -> 180; Surface.ROTATION_270 -> 270; else -> 0 }
        val angle=(sensorOrientation + (if(front) rotation else -rotation) + 360)%360
        val swapped=angle==90 || angle==270
        val scale=maxOf(width.toFloat()/(if(swapped) previewHeight else previewWidth), height.toFloat()/(if(swapped) previewWidth else previewHeight))
        val x=width/2f; val y=height/2f
        setTransform(android.graphics.Matrix().apply {
            setScale(previewWidth.toFloat()/width,previewHeight.toFloat()/height,x,y)
            postRotate(angle.toFloat(),x,y)
            postScale(if(front) -scale else scale,scale,x,y)
        })
    }
    override fun onSurfaceTextureUpdated(surface: SurfaceTexture) {}
    override fun onDetachedFromWindow() { close(); super.onDetachedFromWindow() }
}
