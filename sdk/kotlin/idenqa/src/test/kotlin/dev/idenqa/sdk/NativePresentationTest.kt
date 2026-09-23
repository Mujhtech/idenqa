package dev.idenqa.sdk

import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class NativePresentationTest {
    private val now = Instant.parse("2026-09-22T00:00:00Z")
    private val front = "idenqa.artefact.document_front"
    private val back = "idenqa.artefact.document_back"
    private val requirement = CaptureRequirementDocument("document", "idenqa.purpose.identity_verification",
        "idenqa.evidence.document_image", listOf(back, front),
        CaptureAcquisition("any_of", listOf("idenqa.method.live_camera")), emptyList(), emptyList(),
        listOf(CaptureDocumentOption("passport", "Passport", listOf(front)),
            CaptureDocumentOption("national_id", "National identity card", listOf(back, front))))
    private val session = CaptureSessionDetail("verification", "capture_required", 1, "profile", 1, "digest", "ng-1",
        CaptureProfileDocument(1, CaptureProfileRegistry(1, 1, "digest"), listOf(requirement)), now.plusSeconds(3600))

    private fun authority(action: String? = null, overrides: Map<String, Any> = emptyMap()): ByteArray {
        val a = mapOf("id" to "authority", "subject_id" to "subject", "verification_id" to session.id,
            "notice_id" to "notice", "consent_required" to true, "state" to "active",
            "valid_from" to "2026-01-01T00:00:00Z", "expires_at" to "2027-01-01T00:00:00Z",
            "regions" to listOf("ng-1"), "jurisdiction" to "NG", "retention_reference" to "retention.v1",
            "requirement_purposes" to listOf(requirement.purpose), "evidence_types" to listOf(requirement.evidenceType)) + overrides
        val root = mutableMapOf<String, Any>("authority" to a, "notice" to mapOf("id" to "notice", "locale" to "en-NG",
            "controller" to "Tenant", "recipient" to "Processor", "copy" to mapOf("title" to "Notice",
                "summary" to "Exact <plain> copy", "purpose" to "Verify identity", "consequences" to "You may refuse")))
        if (action != null) root["latest_response"] = mapOf("authority_id" to "authority", "notice_id" to "notice",
            "subject_id" to "subject", "verification_id" to session.id, "action" to action, "locale" to "en-NG")
        return NoticeJson.encode(root)
    }

    @Test fun consentMustBeExplicitAndRefusalDoesNotAuthorizeCapture() {
        val initial = NoticeJson.decode(authority(), session, now)
        assertFalse(initial.accepted)
        assertEquals("Exact <plain> copy", initial.notice.summary)
        assertFalse(NoticeJson.decode(authority("acknowledge"), session, now).accepted)
        assertTrue(NoticeJson.decode(authority("consent"), session, now).accepted)
        val refused = NoticeJson.decode(authority("refuse"), session, now)
        assertTrue(refused.refused)
        assertFalse(refused.accepted)
    }

    @Test fun foreignExpiredAndOutOfScopeAuthorityFailsClosed() {
        for (override in listOf(mapOf("verification_id" to "foreign"), mapOf("notice_id" to "foreign"),
            mapOf("state" to "withdrawn"), mapOf("expires_at" to "2020-01-01T00:00:00Z"),
            mapOf("regions" to listOf("elsewhere")), mapOf("evidence_types" to emptyList<String>()))) {
            assertFailsWith<IdenqaException.StateConflict> { NoticeJson.decode(authority(overrides = override), session, now) }
        }
    }

    @Test fun documentBranchControlsArtefactsAndFrontPrecedesBack() {
        val capabilities = CaptureCapabilities("android", "0.1.0", listOf("idenqa.method.live_camera"),
            listOf("idenqa.method.live_camera"), true, false, false)
        fun plan(type: String) = CapturePlanBuilder.build(session.copy(documentSelections = mapOf("document" to type)),
            capabilities, emptyList(), emptyList(), emptySet())
        assertEquals(listOf(front), plan("passport").tasks.map { it.artefact })
        assertEquals(listOf(front, back), plan("national_id").tasks.map { it.artefact })
        assertFailsWith<IdenqaException.InvalidResponse> { plan("unpublished") }
    }
}
