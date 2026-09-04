package dev.idenqa.sdk

import com.squareup.moshi.Moshi
import java.net.URI
import java.time.Instant
import kotlinx.coroutines.flow.Flow

class IdenqaClient(
    private val baseUri: URI,
    private val tokenStore: CaptureTokenStore,
    private val transport: HttpTransport = OkHttpTransport(),
    private val realtime: RealtimeTransport = OkHttpRealtimeTransport(),
) {
    init {
        if (baseUri.scheme != "https" || baseUri.userInfo != null) throw IdenqaException.InvalidConfiguration
    }

    suspend fun bootstrap(capabilities: CapabilityAdvertisement, identity: NativeBootstrapIdentity, now: Instant = Instant.now()): CaptureSession {
        val token = tokenStore.read()?.takeIf(String::isNotBlank) ?: throw IdenqaException.InvalidConfiguration
        val body = Json.encodeBootstrap(identity.request(token, capabilities, now)).toByteArray()
        val response = transport.send(TransportRequest("POST", baseUri.resolve("v1/capture/native/bootstrap"), mapOf("Authorization" to "Bearer $token", "Content-Type" to "application/json"), body))
        if (response.status != 200) throw IdenqaException.Transport(response.status)
        return Json.decodeSession(response.body.decodeToString())
    }

    suspend fun upload(uri: URI, contentType: String, body: ByteArray) {
        if (uri.scheme != "https" || body.isEmpty()) throw IdenqaException.InvalidConfiguration
        val response = transport.send(TransportRequest("PUT", uri, mapOf("Content-Type" to contentType, "Content-Length" to body.size.toString()), body.copyOf()))
        if (response.status !in 200..299) throw IdenqaException.Transport(response.status)
    }

    fun observe(uri: URI, ticket: String): Flow<RealtimeEvent> = realtime.events(uri, ticket)
}

internal object Json {
    private val adapter = Moshi.Builder().build().adapter(Any::class.java)

    fun encode(value: CapabilityAdvertisement): String = adapter.toJson(mapOf(
        "platform" to value.platform,
        "sdk_version" to value.sdkVersion,
        "implemented_methods" to value.implementedMethods,
        "currently_available_methods" to value.currentlyAvailableMethods,
    ))

    fun encodeBootstrap(value: NativeBootstrapRequest): String = adapter.toJson(linkedMapOf(
        "application_id" to value.applicationId,
        "proof_key" to value.proofKey,
        "proof_created_at" to value.proofCreatedAt,
        "proof_algorithm" to value.proofAlgorithm,
        "proof_format" to value.proofFormat,
        "proof" to value.proof,
        "attestation" to value.attestation,
        "capabilities" to mapOf(
            "platform" to value.capabilities.platform,
            "sdk_version" to value.capabilities.sdkVersion,
            "implemented_methods" to value.capabilities.implementedMethods,
            "currently_available_methods" to value.capabilities.currentlyAvailableMethods,
        ),
    ).filterValues { it != null })

    fun decodeSession(value: String): CaptureSession = try {
        val json = objectValue(value)
        CaptureSession(string(json, "id"), string(json, "state"), number(json, "version"), string(json, "region"), Instant.parse(string(json, "expires_at")))
    } catch (_: Exception) { throw IdenqaException.InvalidResponse }

    fun decodeEvent(value: String): RealtimeEvent = try {
        val json = objectValue(value)
        RealtimeEvent(number(json, "sequence"), string(json, "type"), number(json, "session_version"))
    } catch (_: Exception) { throw IdenqaException.InvalidResponse }

    private fun objectValue(encoded: String): Map<*, *> = adapter.fromJson(encoded) as? Map<*, *> ?: throw IdenqaException.InvalidResponse
    private fun string(value: Map<*, *>, name: String): String = value[name] as? String ?: throw IdenqaException.InvalidResponse
    private fun number(value: Map<*, *>, name: String): Long = (value[name] as? Number)?.toLong() ?: throw IdenqaException.InvalidResponse
}
