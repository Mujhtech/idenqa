package dev.idenqa.sdk

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Matrix
import android.media.ExifInterface

/** Bounded decoding with EXIF normalization, shared by preview and inference. */
internal fun decodeUprightCapture(artifact: CapturedArtifact, maximumDimension: Int): Bitmap {
    if(artifact.contentType!="image/jpeg") throw IdenqaException.InvalidResponse
    val bytes=artifact.bytes()
    try {
        if(bytes.isEmpty() || bytes.size>20_971_520) throw IdenqaException.InvalidResponse
        val bounds=BitmapFactory.Options().apply { inJustDecodeBounds=true }
        BitmapFactory.decodeByteArray(bytes,0,bytes.size,bounds)
        if(bounds.outWidth !in 1..16384 || bounds.outHeight !in 1..16384) throw IdenqaException.InvalidResponse
        var sample=1
        while(maxOf(bounds.outWidth,bounds.outHeight)/sample>maximumDimension*2) sample*=2
        val bitmap=BitmapFactory.decodeByteArray(bytes,0,bytes.size,BitmapFactory.Options().apply { inSampleSize=sample })
            ?: throw IdenqaException.InvalidResponse
        try {
            val orientation=bytes.inputStream().use { ExifInterface(it).getAttributeInt(ExifInterface.TAG_ORIENTATION,ExifInterface.ORIENTATION_NORMAL) }
            val matrix=Matrix().apply {
                when(orientation) {
                    ExifInterface.ORIENTATION_FLIP_HORIZONTAL -> setScale(-1f,1f)
                    ExifInterface.ORIENTATION_ROTATE_180 -> setRotate(180f)
                    ExifInterface.ORIENTATION_FLIP_VERTICAL -> setScale(1f,-1f)
                    ExifInterface.ORIENTATION_TRANSPOSE -> { setRotate(90f); postScale(-1f,1f) }
                    ExifInterface.ORIENTATION_ROTATE_90 -> setRotate(90f)
                    ExifInterface.ORIENTATION_TRANSVERSE -> { setRotate(270f); postScale(-1f,1f) }
                    ExifInterface.ORIENTATION_ROTATE_270 -> setRotate(270f)
                }
            }
            return Bitmap.createBitmap(bitmap,0,0,bitmap.width,bitmap.height,matrix,true).also { if(it!==bitmap) bitmap.recycle() }
        } catch(error: Exception) { bitmap.recycle(); throw error }
    } finally { bytes.fill(0) }
}
