package dev.idenqa.sdk

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

class AndroidKeyStoreCaptureTokenStore(
    context: Context,
    private val alias: String = "dev.idenqa.capture-token.v1",
    preferencesName: String = "dev.idenqa.capture.secure",
) : CaptureTokenStore {
    private val preferences = context.applicationContext.getSharedPreferences(preferencesName, Context.MODE_PRIVATE)
    private val lock = Any()

    init {
        if (alias.isBlank() || preferencesName.isBlank()) throw IdenqaException.InvalidConfiguration
    }

    override suspend fun read(): String? = synchronized(lock) {
        val encoded = preferences.getString(alias, null) ?: return@synchronized null
        try {
            val value = Base64.decode(encoded, Base64.NO_WRAP)
            if (value.size <= 12) throw IdenqaException.SecureStorage
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, value.copyOfRange(0, 12)))
            cipher.doFinal(value.copyOfRange(12, value.size)).decodeToString()
        } catch (error: IdenqaException) {
            throw error
        } catch (_: Exception) {
            throw IdenqaException.SecureStorage
        }
    }

    override suspend fun write(token: String) = synchronized(lock) {
        if (token.isBlank()) throw IdenqaException.InvalidConfiguration
        try {
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.ENCRYPT_MODE, key())
            val encoded = cipher.iv + cipher.doFinal(token.toByteArray())
            if (!preferences.edit().putString(alias, Base64.encodeToString(encoded, Base64.NO_WRAP)).commit()) {
                throw IdenqaException.SecureStorage
            }
        } catch (error: IdenqaException) {
            throw error
        } catch (_: Exception) {
            throw IdenqaException.SecureStorage
        }
    }

    override suspend fun clear() = synchronized(lock) {
        if (!preferences.edit().remove(alias).commit()) throw IdenqaException.SecureStorage
    }

    private fun key(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey(alias, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        generator.init(
            KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .setRandomizedEncryptionRequired(true)
                .build(),
        )
        return generator.generateKey()
    }
}
