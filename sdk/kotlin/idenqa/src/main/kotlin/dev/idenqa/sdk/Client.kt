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
        val response = bootstrapResponse(capabilities, identity, now)
        return Json.decodeSession(response.body.decodeToString())
    }

    suspend fun upload(uri: URI, contentType: String, body: ByteArray) {
        if (uri.scheme != "https" || body.isEmpty()) throw IdenqaException.InvalidConfiguration
        val response = transport.send(TransportRequest("PUT", uri, mapOf("Content-Type" to contentType, "Content-Length" to body.size.toString()), body.copyOf()))
        if (response.status !in 200..299) throw mapStatus(response.status)
    }

    fun observe(uri: URI, ticket: String): Flow<RealtimeEvent> = realtime.events(uri, ticket)

    // MARK: - Journey operations (owned by the capture journey boundary)

    internal suspend fun bootstrapDetail(capabilities: CapabilityAdvertisement, identity: NativeBootstrapIdentity, now: Instant): CaptureSessionDetail {
        val response = bootstrapResponse(capabilities, identity, now)
        return decode(response, JourneyJson::decodeSession)
    }

    internal suspend fun getSession(): CaptureSessionDetail {
        val response = captureRequest("GET", "v1/capture/session")
        return decode(response, JourneyJson::decodeSession)
    }

    internal suspend fun getProgress(ifNoneMatch: String? = null): CaptureProgressResult {
        val headers = if (ifNoneMatch == null) emptyMap() else mapOf("If-None-Match" to ifNoneMatch)
        val response = captureRequest("GET", "v1/capture/progress", headers)
        if (response.status == 304) return CaptureProgressResult(null, response.header("etag"), true)
        val progress = decode(response, JourneyJson::decodeProgress)
        return CaptureProgressResult(progress, response.header("etag"), false)
    }

    internal suspend fun cancel(expectedVersion: Long, idempotencyKey: String): CaptureCancellationDocument {
        if (expectedVersion < 1) throw IdenqaException.InvalidConfiguration
        val response = captureRequest(
            "POST",
            "v1/capture/cancel",
            mapOf("Idempotency-Key" to JourneyJson.structuredString(idempotencyKey)),
            JourneyJson.encodeCancelCommand(expectedVersion).toByteArray(),
        )
        return decode(response, JourneyJson::decodeCancellation)
    }

    internal suspend fun createEvidenceUpload(input: CaptureEvidenceUploadCreate, idempotencyKey: String): EvidenceUploadResult {
        val response = captureRequest(
            "POST",
            "v1/evidence-uploads",
            mapOf("Idempotency-Key" to JourneyJson.structuredString(idempotencyKey)),
            JourneyJson.encodeEvidenceUploadCreate(input).toByteArray(),
        )
        val upload = decode(response, JourneyJson::decodeEvidenceUpload)
        return EvidenceUploadResult(upload, response.header("etag"))
    }

    internal suspend fun uploadEvidence(uploadId: String, contentType: String, body: ByteArray, etag: String, digestHex: String): EvidenceUploadResult {
        if (uploadId.isEmpty() || body.isEmpty() || digestHex.length != 64 || digestHex.any { it.digitToIntOrNull(16) == null }) {
            throw IdenqaException.InvalidConfiguration
        }
        val headers = mapOf(
            "Content-Type" to contentType,
            "Content-Length" to body.size.toString(),
            "Content-Digest" to JourneyJson.contentDigestHeader(digestHex),
            "If-Match" to etag,
        )
        val response = captureRequest("PUT", "v1/evidence-uploads/$uploadId", headers, body.copyOf())
        val upload = decode(response, JourneyJson::decodeEvidenceUpload)
        return EvidenceUploadResult(upload, response.header("etag"))
    }

    // MARK: - Helpers

    private suspend fun bootstrapResponse(capabilities: CapabilityAdvertisement, identity: NativeBootstrapIdentity, now: Instant): TransportResponse {
        val token = tokenStore.read()?.takeIf(String::isNotBlank) ?: throw IdenqaException.InvalidConfiguration
        val body = Json.encodeBootstrap(identity.request(token, capabilities, now)).toByteArray()
        val response = transport.send(TransportRequest("POST", baseUri.resolve("v1/capture/native/bootstrap"), mapOf("Authorization" to "Bearer $token", "Content-Type" to "application/json"), body))
        if (response.status != 200) throw mapStatus(response.status)
        return response
    }

    private suspend fun captureRequest(method: String, path: String, headers: Map<String, String> = emptyMap(), body: ByteArray? = null): TransportResponse {
        val token = tokenStore.read()?.takeIf(String::isNotBlank) ?: throw IdenqaException.InvalidConfiguration
        val values = headers + mapOf("Authorization" to "Bearer $token")
        return transport.send(TransportRequest(method, baseUri.resolve(path), values, body))
    }

    private fun <T> decode(response: TransportResponse, mapper: (String) -> T): T {
        if (response.status !in 200..299) throw mapStatus(response.status)
        return mapper(response.body.decodeToString())
    }

    private fun mapStatus(status: Int): IdenqaException = when (status) {
        401, 403 -> IdenqaException.Unauthenticated
        404 -> IdenqaException.NotFound
        409, 412, 428 -> IdenqaException.StateConflict
        408, 429, 500, 502, 503, 504 -> IdenqaException.Transport(status)
        400, 413, 422 -> IdenqaException.InvalidResponse
        else -> IdenqaException.Transport(status)
    }
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
