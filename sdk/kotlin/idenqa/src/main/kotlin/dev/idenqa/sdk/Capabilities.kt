package dev.idenqa.sdk

/**
 * Honest platform capability advertisement.
 *
 * Capability names guide method selection. They are never assurance evidence:
 * the advertisement never contains provenance, freshness, liveness, or
 * integrity claims, and a currently available method only states that the SDK
 * and device can attempt it.
 */
data class CaptureCapabilities(
    val platform: String,
    val sdkVersion: String,
    val declaredMethods: List<String>,
    val availableMethods: List<String>,
    val cameraCapture: Boolean,
    val fileUpload: Boolean,
    val livenessChallenge: Boolean,
) {
    init {
        if (platform != "ios" && platform != "android") throw IdenqaException.InvalidConfiguration
        if (!CaptureConfiguration.validOpaqueReference(sdkVersion, 64)) throw IdenqaException.InvalidConfiguration
        if (declaredMethods.size > 64 || declaredMethods.toSet().size != declaredMethods.size ||
            declaredMethods.any { !CaptureConfiguration.validMethod(it) }
        ) throw IdenqaException.InvalidConfiguration
        if (availableMethods.size > declaredMethods.size || availableMethods.toSet().size != availableMethods.size ||
            availableMethods.any { it !in declaredMethods }
        ) throw IdenqaException.InvalidConfiguration
    }

    /** The only value sent to Core. */
    fun advertisement(): CapabilityAdvertisement = CapabilityAdvertisement(
        sdkVersion = sdkVersion,
        implementedMethods = declaredMethods,
        currentlyAvailableMethods = availableMethods,
        platform = platform,
    )

    /**
     * Maps platform method names to this device's current availability.
     * Unrecognised methods stay declared but unavailable, so selection fails closed.
     */
    fun withCurrentAvailability(): CaptureCapabilities = copy(
        availableMethods = declaredMethods.filter { method ->
            when (method) {
                "idenqa.method.live_camera" -> cameraCapture
                "idenqa.method.file_upload" -> fileUpload
                else -> false
            }
        },
    )

    companion object {
        /** Current Android capabilities. Camera availability is reported by the host integration. */
        fun current(sdkVersion: String, declaredMethods: List<String>, cameraAvailable: Boolean): CaptureCapabilities =
            CaptureCapabilities(
                platform = "android",
                sdkVersion = sdkVersion,
                declaredMethods = declaredMethods,
                availableMethods = emptyList(),
                cameraCapture = cameraAvailable,
                fileUpload = true,
                livenessChallenge = true,
            ).withCurrentAvailability()
    }
}
