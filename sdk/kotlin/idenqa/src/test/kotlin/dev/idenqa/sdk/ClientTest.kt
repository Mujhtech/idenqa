package dev.idenqa.sdk

import java.net.URI
import java.io.File
import java.time.Instant
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.test.runTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

private class TokenStore : CaptureTokenStore {
    var token: String? = "capture-token"
    override suspend fun read() = token
    override suspend fun write(token: String) { this.token = token }
    override suspend fun clear() { token = null }
}

private class RecordingTransport : HttpTransport {
    val requests = mutableListOf<TransportRequest>()
    override suspend fun send(request: TransportRequest): TransportResponse {
        requests += request
        return TransportResponse(200, """{"id":"ver_1","state":"collecting","version":1,"region":"ng-1","expires_at":"2026-09-05T12:00:00Z"}""".toByteArray())
    }
}

private object EmptyRealtime : RealtimeTransport {
    override fun events(uri: URI, ticket: String): Flow<RealtimeEvent> = emptyFlow()
}

private object ProofKey : NativeProofKey {
    override suspend fun publicKey() = ByteArray(65) { 7 }
    override suspend fun sign(message: ByteArray) = java.security.MessageDigest.getInstance("SHA-256").digest(message)
}

class ClientTest {
    @Test fun bootstrapAdvertisesCapabilitiesWithoutAssurance() = runTest {
        val transport = RecordingTransport()
        val client = IdenqaClient(URI("https://core.example/"), TokenStore(), transport, EmptyRealtime)
        val identity = NativeBootstrapIdentity("dev.idenqa.fixture", ProofKey)
        val session = client.bootstrap(CapabilityAdvertisement("0.1.0", listOf("camera"), emptyList()), identity, Instant.ofEpochSecond(1_788_523_200))
        assertEquals("ng-1", session.region)
        assertEquals("Bearer capture-token", transport.requests.single().headers["Authorization"])
        assertEquals("/v1/capture/native/bootstrap", transport.requests.single().uri.path)
    }

    @Test fun sessionStateRejectsSequenceGaps() {
        val state = CaptureSessionState(CaptureSession("ver_1", "collecting", 1, "ng-1", java.time.Instant.MAX))
        assertFailsWith<IdenqaException.StateConflict> { state.apply(RealtimeEvent(2, "captured", 2)) }
    }

    @Test fun sharedPublishedFixturesDecode() {
        val fixtures = File(System.getProperty("idenqa.repo.root"), "contracts/capture/native/v1/fixtures").canonicalFile
        val session = Json.decodeSession(File(fixtures, "bootstrap-response.json").readText())
        val event = Json.decodeEvent(File(fixtures, "realtime-event.json").readText())
        assertEquals(true, session.id.startsWith("ver_"))
        assertEquals(1, event.sequence)
    }
}
