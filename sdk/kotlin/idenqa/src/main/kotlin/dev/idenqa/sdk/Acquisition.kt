package dev.idenqa.sdk

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.media.FaceDetector
import kotlin.math.abs
import kotlin.math.min
import kotlin.math.sqrt
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.withTimeout

class AcquisitionCoordinator(
    private val source: RawCaptureSource,
    private val assessor: CaptureQualityAssessor,
    private val presenter: ChallengePresenter,
) {
    suspend fun acquire(requirement: CaptureRequirement): List<AcquiredFrame> {
        if (
            requirement.id.isBlank() || requirement.evidenceType.isBlank() || requirement.artefact.isBlank() ||
            requirement.quality.minimumWidth <= 0 || requirement.quality.minimumHeight <= 0 ||
            requirement.quality.maximumBytes <= 0 || requirement.challenges.size > 8
        ) throw IdenqaException.InvalidConfiguration
        val challenges = requirement.challenges.ifEmpty {
            listOf(LivenessChallenge("capture", LivenessPrompt.NEUTRAL, 15_000))
        }
        return challenges.map { challenge ->
            if (challenge.id.isBlank() || challenge.maximumDurationMillis !in 500..15_000) {
                throw IdenqaException.InvalidConfiguration
            }
            val artifact = try {
                withTimeout(challenge.maximumDurationMillis) {
                    presenter.present(challenge)
                    source.capture()
                }
            } catch (_: TimeoutCancellationException) {
                throw IdenqaException.CaptureTimeout
            }
            if (artifact.acquisitionMethod != "idenqa.method.live_camera") throw IdenqaException.InvalidResponse
            val quality = assessor.assess(artifact)
            if (quality.failures(requirement.quality).isNotEmpty()) throw IdenqaException.CaptureQuality
            AcquiredFrame(if (requirement.challenges.isEmpty()) null else challenge.id, artifact, quality)
        }
    }
}

class AndroidImageQualityAssessor : CaptureQualityAssessor {
    override suspend fun assess(artifact: CapturedArtifact): CaptureQualityMeasurement {
        if (artifact.contentType != "image/jpeg") throw IdenqaException.InvalidResponse
        val encoded = artifact.bytes()
        val byteCount = encoded.size
        val decoded = try {
            BitmapFactory.decodeByteArray(encoded, 0, encoded.size)
        } finally {
            encoded.fill(0)
        } ?: throw IdenqaException.InvalidResponse
        val originalWidth = decoded.width
        val originalHeight = decoded.height
        val bitmap = if (decoded.width == 64 && decoded.height == 64) decoded else {
            Bitmap.createScaledBitmap(decoded, 64, 64, true).also { decoded.recycle() }
        }
        try {
            val samples = DoubleArray(bitmap.width * bitmap.height)
            val pixels = IntArray(samples.size)
            bitmap.getPixels(pixels, 0, bitmap.width, 0, 0, bitmap.width, bitmap.height)
            pixels.forEachIndexed { index, pixel ->
                val red = (pixel shr 16) and 0xff
                val green = (pixel shr 8) and 0xff
                val blue = pixel and 0xff
                samples[index] = (0.2126 * red + 0.7152 * green + 0.0722 * blue) / 255
            }
            val mean = samples.average()
            val contrast = min(1.0, sqrt(samples.sumOf { (it - mean) * (it - mean) } / samples.size) * 2)
            var adjacent = 0.0
            for (index in 1 until samples.size) adjacent += abs(samples[index] - samples[index - 1])
            val sharpness = min(1.0, adjacent / (samples.size - 1) * 4)
            val glare = samples.count { it >= 0.98 }.toDouble() / samples.size
            return CaptureQualityMeasurement(
                originalWidth,
                originalHeight,
                byteCount,
                mean,
                contrast,
                sharpness,
                glare,
                faceCount(bitmap),
            )
        } finally {
            bitmap.recycle()
        }
    }

    private fun faceCount(source: Bitmap): Int {
        val bitmap = if (source.config == Bitmap.Config.RGB_565) source else source.copy(Bitmap.Config.RGB_565, false)
        return try {
            val faces = arrayOfNulls<FaceDetector.Face>(4)
            FaceDetector(bitmap.width, bitmap.height, faces.size).findFaces(bitmap, faces)
        } finally {
            if (bitmap !== source) bitmap.recycle()
        }
    }
}
