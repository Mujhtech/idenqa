package dev.idenqa.sdk

import android.view.Window
import android.view.WindowManager

/**
 * Sensitive-screen protection port.
 *
 * The port never fails a capture journey when the platform cannot protect a
 * surface: `isAvailable` reports the honest state and the journey degrades to
 * guidance. `isCaptured` reports recording/mirroring state when the platform
 * exposes it; Android does not provide a reliable capture flag, so the default
 * implementation reports false rather than guessing.
 */
interface ScreenProtection {
    val isAvailable: Boolean
    suspend fun applyProtection()
    suspend fun removeProtection()
    suspend fun isCaptured(): Boolean
}

/** Graceful default used when a platform protection surface is unavailable. */
object NoopScreenProtection : ScreenProtection {
    override val isAvailable: Boolean get() = false
    override suspend fun applyProtection() {}
    override suspend fun removeProtection() {}
    override suspend fun isCaptured(): Boolean = false
}

/**
 * Applies `FLAG_SECURE` to one host window while a capture preview is visible.
 * The flag prevents screenshots, screen recording, and non-secure displays.
 * Where an OEM rejects the flag the adapter degrades to `isAvailable = false`
 * rather than failing the journey.
 */
class AndroidWindowScreenProtection(private val window: Window) : ScreenProtection {
    private var applied = false

    private var rejected = false

    override val isAvailable: Boolean get() = !rejected

    override suspend fun applyProtection() {
        if (rejected) return
        try {
            window.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
            applied = true
        } catch (_: Exception) {
            rejected = true
            applied = false
        }
    }

    override suspend fun removeProtection() {
        if (!applied) return
        try {
            window.clearFlags(WindowManager.LayoutParams.FLAG_SECURE)
        } catch (_: Exception) {
            rejected = true
        } finally {
            applied = false
        }
    }

    override suspend fun isCaptured(): Boolean = false
}
