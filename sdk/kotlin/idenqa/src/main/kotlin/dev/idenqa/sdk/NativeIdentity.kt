package dev.idenqa.sdk

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyInfo
import android.security.keystore.KeyProperties
import java.security.KeyFactory
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.MessageDigest
import java.security.PrivateKey
import java.security.Signature
import java.security.spec.ECGenParameterSpec
import java.time.Instant
import java.util.Base64

interface NativeProofKey {
    suspend fun publicKey(): ByteArray
    suspend fun sign(message: ByteArray): ByteArray
}

fun interface NativeAttestationProvider { suspend fun attestation(): String? }

class AndroidP256ProofKey(private val alias: String = "dev.idenqa.native-proof.v1") : NativeProofKey {
    init { if (alias.isBlank()) throw IdenqaException.InvalidConfiguration }

    override suspend fun publicKey(): ByteArray = pair().certificate.publicKey.encoded.copyOf()

    override suspend fun sign(message: ByteArray): ByteArray {
        val signature = Signature.getInstance("SHA256withECDSA")
        signature.initSign(pair().privateKey)
        signature.update(message)
        return signature.sign()
    }

    private fun pair(): KeyStore.PrivateKeyEntry {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getEntry(alias, null) as? KeyStore.PrivateKeyEntry)?.let {
            requireHardwareBacked(it.privateKey)
            return it
        }
        val generator = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, "AndroidKeyStore")
        generator.initialize(
            KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_SIGN)
                .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
                .setDigests(KeyProperties.DIGEST_SHA256)
                .setUserAuthenticationRequired(false)
                .build(),
        )
        generator.generateKeyPair()
        return (store.getEntry(alias, null) as? KeyStore.PrivateKeyEntry)?.also { requireHardwareBacked(it.privateKey) }
            ?: throw IdenqaException.SecureStorage
    }

    private fun requireHardwareBacked(key: PrivateKey) {
        val factory = KeyFactory.getInstance(key.algorithm, "AndroidKeyStore")
        val info = factory.getKeySpec(key, KeyInfo::class.java)
        @Suppress("DEPRECATION")
        if (!info.isInsideSecureHardware) throw IdenqaException.HardwareSecurityUnavailable
    }
}

class NativeBootstrapIdentity(
    val applicationId: String,
    private val key: NativeProofKey,
    private val attestationProvider: NativeAttestationProvider? = null,
) {
    init {
        if (applicationId.isBlank() || applicationId.length > 255 || !applicationId.all { it.isLetterOrDigit() || it in "._-" }) {
            throw IdenqaException.InvalidConfiguration
        }
    }

    companion object {
        fun installedApplication(context: Context, key: NativeProofKey, attestationProvider: NativeAttestationProvider? = null) =
            NativeBootstrapIdentity(context.applicationContext.packageName, key, attestationProvider)
    }

    internal suspend fun request(token: String, capabilities: CapabilityAdvertisement, createdAt: Instant): NativeBootstrapRequest {
        val created = createdAt.epochSecond
        if (created <= 0) throw IdenqaException.InvalidConfiguration
        val tuple = capabilities.platform + "\u0000" + capabilities.sdkVersion + "\u0000" + capabilities.implementedMethods.sorted().joinToString("\u001f") + "\u0000" + capabilities.currentlyAvailableMethods.sorted().joinToString("\u001f")
        val canonical = "idq-native-bootstrap\u0000v1\u0000$applicationId\u0000POST\u0000/v1/capture/native/bootstrap\u0000$created\u0000${sha256(token.toByteArray())}\u0000${sha256(tuple.toByteArray())}"
        return NativeBootstrapRequest(
            applicationId, key.publicKey().base64Url(), created, "ES256", "der",
            key.sign(canonical.toByteArray()).base64Url(), attestationProvider?.attestation(), capabilities,
        )
    }

    private fun sha256(value: ByteArray) = MessageDigest.getInstance("SHA-256").digest(value).joinToString("") { "%02x".format(it) }
    private fun ByteArray.base64Url() = Base64.getUrlEncoder().withoutPadding().encodeToString(this)
}

internal data class NativeBootstrapRequest(
    val applicationId: String,
    val proofKey: String,
    val proofCreatedAt: Long,
    val proofAlgorithm: String,
    val proofFormat: String,
    val proof: String,
    val attestation: String?,
    val capabilities: CapabilityAdvertisement,
)
