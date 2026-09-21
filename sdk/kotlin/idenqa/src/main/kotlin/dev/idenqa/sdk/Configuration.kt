package dev.idenqa.sdk

import java.net.URI

/**
 * Bounded, non-secret capture SDK configuration.
 *
 * The configuration deliberately has no token, API-key, or credential field.
 * Capture credentials are accepted only through the bootstrap credential flow
 * (`CaptureJourney.start`) and are stored through the platform secure store.
 */
data class CaptureConfiguration(
    val coreUri: URI,
    val applicationId: String,
    val installationId: String,
    val locale: String,
    val region: String? = null,
    val captureProfileId: String? = null,
    val preferredMethods: List<String> = emptyList(),
    val maximumUploadBytes: Int = 16 * 1024 * 1024,
    val maximumTemporaryBytes: Int = 64 * 1024 * 1024,
) {
    init {
        if (coreUri.scheme != "https" || coreUri.userInfo != null || coreUri.host == null ||
            coreUri.rawQuery != null || coreUri.rawFragment != null
        ) throw IdenqaException.InvalidConfiguration
        if (!validOpaqueReference(applicationId, 255)) throw IdenqaException.InvalidConfiguration
        if (!validOpaqueReference(installationId, 128)) throw IdenqaException.InvalidConfiguration
        if (!validLocale(locale)) throw IdenqaException.InvalidConfiguration
        if (region != null && !validRegion(region)) throw IdenqaException.InvalidConfiguration
        if (captureProfileId != null && (!validOpaqueReference(captureProfileId, 160) || !captureProfileId.startsWith("prf_"))) {
            throw IdenqaException.InvalidConfiguration
        }
        if (preferredMethods.size > 64 || preferredMethods.toSet().size != preferredMethods.size ||
            preferredMethods.any { !validMethod(it) }
        ) throw IdenqaException.InvalidConfiguration
        if (maximumUploadBytes !in 1_024..67_108_864) throw IdenqaException.InvalidConfiguration
        if (maximumTemporaryBytes < maximumUploadBytes || maximumTemporaryBytes > 268_435_456) {
            throw IdenqaException.InvalidConfiguration
        }
    }

    companion object {
        /** Bounded, non-empty ASCII reference using the native bootstrap alphabet. */
        fun validOpaqueReference(value: String, maximum: Int): Boolean =
            value.isNotEmpty() && value.length <= maximum && value.all { it.isLetterOrDigit() || it in "._-" } &&
                value.all { it.code < 128 }

        /** Namespaced acquisition-method name. Unknown namespaced methods fail closed during selection. */
        fun validMethod(value: String): Boolean =
            validOpaqueReference(value, 128) && value.contains('.') && !value.startsWith(".") && !value.endsWith(".")

        fun validRegion(value: String): Boolean =
            value.length <= 63 && value.isNotEmpty() && value.first() in 'a'..'z' &&
                value.all { it in 'a'..'z' || it in '0'..'9' || it == '-' }

        fun validLocale(value: String): Boolean {
            if (value.length > 64) return false
            val parts = value.split('-')
            val language = parts.firstOrNull() ?: return false
            if (language.length !in 2..8 || language.any { !it.isLetter() || it.code >= 128 }) return false
            return parts.drop(1).all { part ->
                part.length in 1..8 && part.all { (it.isLetter() || it.isDigit()) && it.code < 128 }
            }
        }
    }
}
