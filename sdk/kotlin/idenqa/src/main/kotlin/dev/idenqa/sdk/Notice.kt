package dev.idenqa.sdk

import com.squareup.moshi.Moshi
import java.time.Instant

/** Exact Core-authored copy, always rendered as plain text. */
data class CaptureNotice(
    val id: String, val locale: String, val controller: String, val recipient: String,
    val title: String, val summary: String, val purpose: String, val consequences: String,
)
enum class CaptureNoticeAction(val wire: String) { ACKNOWLEDGE("acknowledge"), CONSENT("consent"), REFUSE("refuse") }
data class CaptureNoticeState(
    val notice: CaptureNotice, val consentRequired: Boolean, val accepted: Boolean, val refused: Boolean,
    val jurisdiction: String, val retentionReference: String, val regions: List<String>,
)
data class CaptureDocumentOption(val id: String, val label: String, val artefacts: List<String>)
data class CaptureDocumentChoice(val requirementKey: String, val options: List<CaptureDocumentOption>)

internal object NoticeJson {
    private val adapter = Moshi.Builder().build().adapter(Any::class.java)
    fun encode(value: Map<String, Any>): ByteArray = adapter.toJson(value).toByteArray()
    fun decode(encoded: ByteArray, session: CaptureSessionDetail, now: Instant): CaptureNoticeState {
        try {
            val root = map(adapter.fromJson(encoded.decodeToString()))
            val a = map(root["authority"]); val n = map(root["notice"]); val copy = map(n["copy"])
            val notice = CaptureNotice(string(n,"id"),string(n,"locale"),string(n,"controller"),string(n,"recipient"),
                string(copy,"title"),string(copy,"summary"),string(copy,"purpose"),string(copy,"consequences"))
            val required = a["consent_required"] as? Boolean ?: throw IdenqaException.InvalidResponse
            val regions = strings(a,"regions")
            if (string(a,"verification_id") != session.id || string(a,"notice_id") != notice.id ||
                string(a,"state") != "active" || now < Instant.parse(string(a,"valid_from")) ||
                now >= Instant.parse(string(a,"expires_at")) || session.region !in regions || notice.locale.isBlank() ||
                session.requirements.requirements.any { it.purpose !in strings(a,"requirement_purposes") || it.evidenceType !in strings(a,"evidence_types") }) {
                throw IdenqaException.StateConflict
            }
            val response = root["latest_response"]?.let(::map)
            if (response != null && (string(response,"authority_id") != string(a,"id") ||
                string(response,"notice_id") != notice.id || string(response,"subject_id") != string(a,"subject_id") ||
                string(response,"verification_id") != session.id || string(response,"locale") != notice.locale ||
                string(response,"action") !in listOf("consent","acknowledge","refuse"))) throw IdenqaException.InvalidResponse
            return CaptureNoticeState(notice, required, response?.get("action") == if(required) "consent" else "acknowledge",
                response?.get("action") == "refuse", string(a,"jurisdiction"),string(a,"retention_reference"),regions)
        } catch (error: IdenqaException) { throw error }
        catch (_: Exception) { throw IdenqaException.InvalidResponse }
    }
    private fun map(value: Any?): Map<*, *> = value as? Map<*, *> ?: throw IdenqaException.InvalidResponse
    private fun string(value: Map<*, *>, key: String): String = value[key] as? String ?: throw IdenqaException.InvalidResponse
    private fun strings(value: Map<*, *>, key: String): List<String> = (value[key] as? List<*>)?.map { it as? String ?: throw IdenqaException.InvalidResponse } ?: throw IdenqaException.InvalidResponse
}

internal suspend fun IdenqaClient.getAuthority(session: CaptureSessionDetail, now: Instant): CaptureNoticeState {
    val response = captureRequest("GET", "v1/capture/authority")
    if (response.status !in 200..299) throw IdenqaException.Transport(response.status)
    return NoticeJson.decode(response.body,session,now)
}
internal suspend fun IdenqaClient.respond(action: CaptureNoticeAction, locale: String, key: String) {
    val response = captureRequest("POST","v1/capture/authority/responses",
        mapOf("Content-Type" to "application/json","Idempotency-Key" to JourneyJson.structuredString(key)),
        NoticeJson.encode(mapOf("action" to action.wire,"locale" to locale)))
    if (response.status !in 200..299) throw IdenqaException.Transport(response.status)
}
internal suspend fun IdenqaClient.selectDocument(requirement: String, type: String, version: Long, key: String): CaptureSessionDetail {
    val response = captureRequest("POST","v1/capture/document-selection",
        mapOf("Content-Type" to "application/json","Idempotency-Key" to JourneyJson.structuredString(key)),
        NoticeJson.encode(mapOf("requirement_key" to requirement,"document_type" to type,"expected_version" to version)))
    if (response.status !in 200..299) throw IdenqaException.Transport(response.status)
    return JourneyJson.decodeSession(response.body.decodeToString())
}
