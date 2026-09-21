package dev.idenqa.sdk

import java.io.File
import java.io.IOException
import java.net.URI
import java.nio.file.Files
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.test.runTest

// MARK: - Fakes

private class ScriptedTransport(handler: suspend (TransportRequest) -> TransportResponse) : HttpTransport {
    var handler: suspend (TransportRequest) -> TransportResponse = handler
    val requests = mutableListOf<TransportRequest>()
    override suspend fun send(request: TransportRequest): TransportResponse {
        requests += request
        return handler(request)
    }
}

private class MemoryTokenStore : CaptureTokenStore {
    var token: String? = null
    override suspend fun read(): String? = token
    override suspend fun write(token: String) { this.token = token }
    override suspend fun clear() { token = null }
}

private class MemoryJourneyStore : CaptureJourneyStore {
    var value: CaptureJourneyReference? = null
    override suspend fun read(): CaptureJourneyReference? = value
    override suspend fun write(reference: CaptureJourneyReference) { value = reference }
    override suspend fun clear() { value = null }
}

private class MemoryTemporaryFiles : CaptureTemporaryFileStore {
    val files = mutableMapOf<String, ByteArray>()
    val removedReferences = mutableListOf<String>()
    private var stages = 0

    override suspend fun stage(artifact: CapturedArtifact, category: String, now: Instant): CaptureTemporaryFile {
        if (artifact.contentType != "image/jpeg" || artifact.bytes().isEmpty()) throw IdenqaException.TemporaryStorage
        stages += 1
        val reference = "$category-$stages.bin"
        files[reference] = artifact.bytes()
        return CaptureTemporaryFile(reference, artifact.bytes().size, now)
    }

    override suspend fun data(file: CaptureTemporaryFile): ByteArray =
        files[file.reference] ?: throw IdenqaException.InvalidConfiguration

    override suspend fun remove(file: CaptureTemporaryFile) {
        files.remove(file.reference)
        removedReferences += file.reference
    }

    override suspend fun purge(olderThan: Instant): Int {
        val count = files.size
        removedReferences += files.keys
        files.clear()
        return count
    }

    override suspend fun purgeAll(): Int {
        val count = files.size
        removedReferences += files.keys
        files.clear()
        return count
    }

    override suspend fun count(): Int = files.size
    override suspend fun totalBytes(): Int = files.values.sumOf { it.size }
}

private class FixtureProofKey : NativeProofKey {
    override suspend fun publicKey(): ByteArray = ByteArray(65) { 7 }
    override suspend fun sign(message: ByteArray): ByteArray =
        java.security.MessageDigest.getInstance("SHA-256").digest(message)
}

private class TrackingProofKey : NativeProofKey {
    var cleared = false
    override suspend fun publicKey(): ByteArray = ByteArray(65) { 7 }
    override suspend fun sign(message: ByteArray): ByteArray =
        java.security.MessageDigest.getInstance("SHA-256").digest(message)
    override suspend fun clear() { cleared = true }
    override suspend fun isCleared(): Boolean = cleared
}

private class FakeScreenProtection : ScreenProtection {
    override var isAvailable: Boolean = true
    var protected = false
    var captured = false
    override suspend fun applyProtection() { protected = true }
    override suspend fun removeProtection() { protected = false }
    override suspend fun isCaptured(): Boolean = captured
}

private object EmptyRealtimeTransport : RealtimeTransport {
    override fun events(uri: URI, ticket: String): Flow<RealtimeEvent> = emptyFlow()
}

private class TestTimeSource : CaptureTimeSource {
    private val value = Instant.ofEpochSecond(1_788_523_200)
    override fun now(): Instant = value
}

private class KeySequence {
    private var counter = 0
    fun make(prefix: String): String {
        counter += 1
        return "${prefix}_fixture000$counter"
    }
}

private data class FixtureWorld(
    val journey: CaptureJourney,
    val tokenStore: MemoryTokenStore,
    val referenceStore: MemoryJourneyStore,
    val temporaryFiles: MemoryTemporaryFiles,
    val proofKey: TrackingProofKey,
    val transport: ScriptedTransport,
    val core: FakeCore,
)

private fun makeWorld(
    core: FakeCore,
    declaredMethods: List<String> = listOf("idenqa.method.live_camera", "idenqa.method.file_upload"),
    availableMethods: List<String> = listOf("idenqa.method.live_camera", "idenqa.method.file_upload"),
    pinnedProfile: String? = null,
    pinnedRegion: String? = null,
    temporaryFiles: CaptureTemporaryFileStore? = null,
    screenProtection: ScreenProtection = NoopScreenProtection,
    keys: KeySequence = KeySequence(),
): FixtureWorld {
    val tokenStore = MemoryTokenStore()
    val referenceStore = MemoryJourneyStore()
    val temporary = MemoryTemporaryFiles()
    val proofKey = TrackingProofKey()
    val transport = ScriptedTransport { request -> core.handle(request) }
    val configuration = CaptureConfiguration(
        coreUri = URI("https://core.example"),
        applicationId = "dev.idenqa.fixture",
        installationId = "install-1",
        locale = "en-NG",
        region = pinnedRegion,
        captureProfileId = pinnedProfile,
    )
    val journey = CaptureJourney(
        configuration = configuration,
        initialCapabilities = CaptureCapabilities(
            platform = "android",
            sdkVersion = "0.1.0",
            declaredMethods = declaredMethods,
            availableMethods = availableMethods,
            cameraCapture = "idenqa.method.live_camera" in availableMethods,
            fileUpload = "idenqa.method.file_upload" in availableMethods,
            livenessChallenge = true,
        ),
        tokenStore = tokenStore,
        referenceStore = referenceStore,
        proofKey = proofKey,
        transport = transport,
        realtime = EmptyRealtimeTransport,
        temporaryFiles = temporaryFiles ?: temporary,
        screenProtection = screenProtection,
        time = TestTimeSource(),
        idempotencyKeyFactory = { prefix -> keys.make(prefix) },
    )
    return FixtureWorld(journey, tokenStore, referenceStore, temporary, proofKey, transport, core)
}

// MARK: - Fake Core

private class FakeCore(private val requirements: String = defaultRequirements()) {
    var state = "collecting"
    var version = 1L
    val completions = mutableListOf<Map<String, String>>()
    val uploadHeaders = mutableListOf<Map<String, String>>()
    val uploadBodies = mutableListOf<ByteArray>()
    val issueKeys = mutableListOf<String>()
    val cancelKeys = mutableListOf<String>()
    var cancelMutations = 0
    private val cancelReceipts = mutableMapOf<String, String>()
    var uploadStatusOverride: Int? = null
    var uploadNetworkFailure = false
    var nextCancelNetworkFailure = false

    suspend fun handle(request: TransportRequest): TransportResponse {
        val path = request.uri.path
        return when (request.method to path) {
            "POST" to "/v1/capture/native/bootstrap" ->
                TransportResponse(200, sessionJson().toByteArray(), mapOf("cache-control" to "no-store"))

            "GET" to "/v1/capture/session" -> TransportResponse(200, sessionJson().toByteArray())

            "GET" to "/v1/capture/progress" -> {
                if (nextProgressNetworkFailure) {
                    nextProgressNetworkFailure = false
                    throw IOException("offline")
                }
                val etag = progressEtag()
                if (request.headers["If-None-Match"] == etag) TransportResponse(304, ByteArray(0), mapOf("etag" to etag))
                else TransportResponse(200, progressJson().toByteArray(), mapOf("etag" to etag))
            }

            "POST" to "/v1/capture/cancel" -> {
                if (nextCancelNetworkFailure) {
                    nextCancelNetworkFailure = false
                    throw IOException("offline")
                }
                val key = request.headers["Idempotency-Key"] ?: return TransportResponse(400, ByteArray(0))
                cancelReceipts[key]?.let { return TransportResponse(200, it.toByteArray()) }
                val expected = request.body?.decodeToString()?.substringAfter("\"expected_version\":")?.takeWhile { it.isDigit() }?.toLongOrNull() ?: 0L
                if (expected != version) return TransportResponse(409, ByteArray(0))
                cancelMutations += 1
                cancelKeys += key
                state = "cancelled"
                version += 1
                val receipt = cancellationJson()
                cancelReceipts[key] = receipt
                TransportResponse(200, receipt.toByteArray())
            }

            "POST" to "/v1/evidence-uploads" -> {
                val key = request.headers["Idempotency-Key"] ?: return TransportResponse(400, ByteArray(0))
                issueKeys += key
                TransportResponse(201, uploadJson().toByteArray(), mapOf("etag" to "\"1\""))
            }

            "PUT" to "/v1/evidence-uploads/upl_01J00000000000000000000000" -> {
                if (uploadNetworkFailure) {
                    uploadNetworkFailure = false
                    throw IOException("offline")
                }
                uploadStatusOverride?.let { return TransportResponse(it, ByteArray(0)) }
                val body = request.body ?: return TransportResponse(400, ByteArray(0))
                uploadBodies += body
                uploadHeaders += request.headers
                accepted = true
                completions += mapOf(
                    "upload_id" to "upl_01J00000000000000000000000",
                    "evidence_id" to "evd_01J00000000000000000000000",
                    "requirement_key" to "selfie",
                    "evidence_type" to "idenqa.evidence.selfie_image",
                    "artefact" to "idenqa.artefact.selfie_image",
                    "acquisition_method" to "idenqa.method.live_camera",
                )
                TransportResponse(200, uploadJson().toByteArray(), mapOf("etag" to "\"3\""))
            }

            else -> TransportResponse(404, ByteArray(0))
        }
    }

    private var accepted = false
    var nextProgressNetworkFailure = false

    fun sessionJson(): String = """
        {"id":"ver_01J00000000000000000000000","state":"$state","version":$version,
         "profile_id":"prf_01J00000000000000000000000","profile_revision":1,
         "profile_digest":"sha256:${"a".repeat(64)}","policy_id":"pol_01J00000000000000000000000",
         "region":"ng-1","requirements":$requirements,
         "created_at":"2026-09-04T12:00:00Z","updated_at":"2026-09-04T12:00:00Z",
         "expires_at":"2026-09-05T12:00:00Z"}
    """.trimIndent().replace("\n", "")

    private fun progressJson(): String {
        val entries = completions.joinToString(",") { completion ->
            completion.entries.joinToString(",") { (key, value) -> "\"$key\":\"$value\"" }.let { "{$it}" }
        }
        return """{"verification_id":"ver_01J00000000000000000000000","completions":[$entries]}"""
    }

    private fun progressEtag(): String = "\"${completions.size}-${completions.hashCode()}\""

    private fun uploadJson(): String = """
        {"id":"upl_01J00000000000000000000000","evidence_id":"evd_01J00000000000000000000000",
         "state":"${if (accepted) "accepted" else "issued"}","version":${if (accepted) 3 else 1},
         "attempt":${if (accepted) 1 else 0},"requirement_key":"selfie",
         "evidence_type":"idenqa.evidence.selfie_image","artefact":"idenqa.artefact.selfie_image",
         "acquisition_method":"idenqa.method.live_camera","assurances":[],
         "allowed_media_types":["image/jpeg"],"maximum_bytes":16777216,"expected_bytes":3,
         "media_type":"image/jpeg","region":"ng-1","created_at":"2026-09-04T12:00:00Z",
         "updated_at":"2026-09-04T12:00:00Z","expires_at":"2026-09-04T12:15:00Z"}
    """.trimIndent().replace("\n", "")

    private fun cancellationJson(): String = """{"event_id":"evt_01J00000000000000000000000","verification_id":"ver_01J00000000000000000000000","state":"cancelled","version":$version,"occurred_at":"2026-09-04T12:00:00Z"}"""

    companion object {
        fun defaultRequirements(): String = requirements(
            requirement("selfie", "idenqa.evidence.selfie_image", "idenqa.artefact.selfie_image", methods = listOf("idenqa.method.live_camera")) +
                "," +
                requirement("document", "idenqa.evidence.document_image", "idenqa.artefact.document_front", methods = listOf("idenqa.method.live_camera")),
        )

        fun requirements(vararg values: String): String =
            """{"schema_version":1,"registry":{"schema_version":1,"revision":1,"digest":"sha256:${"b".repeat(64)}"},"requirements":[${values.joinToString(",")}]}"""

        fun requirement(
            key: String,
            evidenceType: String,
            artefact: String,
            strategy: String = "any_of",
            methods: List<String>,
            fallbacks: String = "",
        ): String = """
            {"key":"$key","purpose":"idenqa.purpose.identity_verification","evidence_type":"$evidenceType",
             "artefacts":["$artefact"],"acquisition":{"strategy":"$strategy","methods":${methods.joinToString(",", "[", "]") { "\"$it\"" }}},
             "required_assurances":[],"fallbacks":[$fallbacks]}
        """.trimIndent().replace("\n", "")
    }
}

// MARK: - Tests

class JourneyTest {
    @Test fun configurationValidatesBoundsAndRejectsSecrets() {
        assertFailure(IdenqaException.InvalidConfiguration) {
            CaptureConfiguration(URI("http://core.example"), "dev.idenqa.fixture", "install-1", "en-NG")
        }
        assertFailure(IdenqaException.InvalidConfiguration) {
            CaptureConfiguration(URI("https://core.example"), "dev.idenqa.fixture", "install-1", "not a locale")
        }
        assertFailure(IdenqaException.InvalidConfiguration) {
            CaptureConfiguration(
                URI("https://core.example"), "dev.idenqa.fixture", "install-1", "en",
                preferredMethods = listOf("camera"),
            )
        }
        val configuration = CaptureConfiguration(
            URI("https://core.example"), "dev.idenqa.fixture", "install-1", "en-NG", region = "ng-1",
        )
        assertEquals("ng-1", configuration.region)
    }

    @Test fun startBootstrapsAndRendersOneTaskPerScreen() = runTest {
        val core = FakeCore()
        val world = makeWorld(core)
        val snapshot = world.journey.start("idq_cap_v1_fixture")
        assertEquals(CaptureJourneyStatus.CAPTURE_REQUIRED, snapshot.status)
        assertEquals("selfie", snapshot.currentTask?.requirementKey)
        assertEquals(2, snapshot.remainingTaskCount)
        assertEquals(0, snapshot.completedTaskCount)
        assertTrue(snapshot.canCancel)
        assertEquals("idq_cap_v1_fixture", world.tokenStore.read())
        assertNotNull(world.referenceStore.read())
    }

    @Test fun resumeAfterProcessDeathReattachesWithoutBootstrap() = runTest {
        val core = FakeCore()
        val world = makeWorld(core)
        world.journey.start("idq_cap_v1_fixture")
        assertTrue(world.transport.requests.any { it.uri.path == "/v1/capture/native/bootstrap" })
        val restarted = makeWorld(core)
        restarted.tokenStore.write("idq_cap_v1_fixture")
        restarted.referenceStore.write(assertNotNull(world.referenceStore.read()))
        val snapshot = restarted.journey.resume()
        assertEquals(CaptureJourneyStatus.CAPTURE_REQUIRED, snapshot.status)
        assertEquals("selfie", snapshot.currentTask?.requirementKey)
        assertFalse(restarted.transport.requests.any { it.uri.path == "/v1/capture/native/bootstrap" })
        assertTrue(restarted.transport.requests.any { it.uri.path == "/v1/capture/session" })
    }

    @Test fun resumeWithoutPersistedReferenceFailsClosed() = runTest {
        val world = makeWorld(FakeCore())
        assertEquals(IdenqaException.NotStarted, runCatching { world.journey.resume() }.exceptionOrNull())
    }

    @Test fun resumeRestoresAuthoritativeAcceptedProgress() = runTest {
        val core = FakeCore()
        core.completions += mapOf(
            "upload_id" to "upl_01J00000000000000000000000",
            "evidence_id" to "evd_01J00000000000000000000000",
            "requirement_key" to "selfie",
            "evidence_type" to "idenqa.evidence.selfie_image",
            "artefact" to "idenqa.artefact.selfie_image",
            "acquisition_method" to "idenqa.method.live_camera",
        )
        val world = makeWorld(core)
        world.journey.start("idq_cap_v1_fixture")
        val snapshot = world.journey.resume()
        assertEquals(1, snapshot.completedTaskCount)
        assertEquals("document", snapshot.currentTask?.requirementKey)
    }

    @Test fun submitUploadsWithBoundDigestAndCleansTemporaryFile() = runTest {
        val core = FakeCore()
        val world = makeWorld(core)
        val started = world.journey.start("idq_cap_v1_fixture")
        val task = assertNotNull(started.currentTask)
        val artifact = CapturedArtifact(byteArrayOf(1, 2, 3), "image/jpeg", "idenqa.method.live_camera")
        val snapshot = world.journey.submit(task.id, artifact, "idenqa.method.live_camera")
        assertEquals("document", snapshot.currentTask?.requirementKey)
        assertEquals(1, snapshot.completedTaskCount)
        assertEquals(0, world.temporaryFiles.count())
        val headers = assertNotNull(core.uploadHeaders.firstOrNull())
        assertEquals("\"1\"", headers["If-Match"])
        assertTrue(headers["Content-Digest"]!!.startsWith("sha-256=:"))
        assertTrue(core.issueKeys.first().startsWith("\"capture_upload_"))
        assertEquals(IdenqaException.StateConflict, runCatching {
            world.journey.submit(task.id, artifact, "idenqa.method.live_camera")
        }.exceptionOrNull())
    }

    @Test fun submitCleansTemporaryFileOnFailure() = runTest {
        val core = FakeCore()
        core.uploadStatusOverride = 409
        val world = makeWorld(core)
        val started = world.journey.start("idq_cap_v1_fixture")
        val task = assertNotNull(started.currentTask)
        val artifact = CapturedArtifact(byteArrayOf(1, 2, 3), "image/jpeg", "idenqa.method.live_camera")
        assertEquals(IdenqaException.StateConflict, runCatching {
            world.journey.submit(task.id, artifact, "idenqa.method.live_camera")
        }.exceptionOrNull())
        assertEquals(0, world.temporaryFiles.count())
        assertEquals(1, world.temporaryFiles.removedReferences.size)
    }

    @Test fun submitReportsNetworkFailureAndKeepsProgress() = runTest {
        val core = FakeCore()
        core.uploadNetworkFailure = true
        val world = makeWorld(core)
        val started = world.journey.start("idq_cap_v1_fixture")
        val task = assertNotNull(started.currentTask)
        val artifact = CapturedArtifact(byteArrayOf(1, 2, 3), "image/jpeg", "idenqa.method.live_camera")
        assertEquals(IdenqaException.NetworkUnavailable, runCatching {
            world.journey.submit(task.id, artifact, "idenqa.method.live_camera")
        }.exceptionOrNull())
        val snapshot = world.journey.currentSnapshot()
        assertEquals(CaptureGuidanceCode.NETWORK_UNAVAILABLE, snapshot.guidance.code)
        assertTrue(snapshot.canCancel)
        assertEquals(0, world.temporaryFiles.count())
    }

    @Test fun cancelRetriesWithTheSamePersistedIdempotencyKey() = runTest {
        val core = FakeCore()
        val world = makeWorld(core)
        world.journey.start("idq_cap_v1_fixture")
        core.nextCancelNetworkFailure = true
        assertEquals(IdenqaException.NetworkUnavailable, runCatching { world.journey.cancel() }.exceptionOrNull())
        val receipt = world.journey.cancel()
        assertEquals("cancelled", receipt.state)
        assertEquals(1, core.cancelKeys.size)
        assertTrue(core.cancelKeys[0].startsWith("\"capture_cancel_"))

        val restarted = makeWorld(core)
        restarted.referenceStore.write(assertNotNull(world.referenceStore.read()))
        restarted.tokenStore.write("idq_cap_v1_fixture")
        val replay = restarted.journey.cancel()
        assertEquals(receipt.eventId, replay.eventId)
        assertEquals(1, core.cancelMutations)
    }

    @Test fun cancelConflictReconcilesTheAuthoritativeTerminalState() = runTest {
        val core = FakeCore()
        core.version = 9
        val world = makeWorld(core)
        world.journey.start("idq_cap_v1_fixture")
        core.version = 11
        assertEquals(IdenqaException.StateConflict, runCatching { world.journey.cancel() }.exceptionOrNull())
        assertEquals(11L, world.journey.currentSnapshot().sessionVersion)
    }

    @Test fun clearLocalDataIsCompleteAndIdempotent() = runTest {
        val core = FakeCore()
        val world = makeWorld(core)
        world.journey.start("idq_cap_v1_fixture")
        world.temporaryFiles.stage(
            CapturedArtifact(byteArrayOf(9, 9), "image/jpeg", "idenqa.method.live_camera"), "evidence", Instant.now(),
        )
        val report = world.journey.clearLocalData()
        assertTrue(report.allCleared)
        assertEquals(1, report.temporaryFilesRemoved)
        assertNull(world.tokenStore.read())
        assertNull(world.referenceStore.read())
        assertTrue(world.proofKey.isCleared())
        assertEquals(0, world.temporaryFiles.count())
        val second = world.journey.clearLocalData()
        assertTrue(second.allCleared)
        assertEquals(0, second.temporaryFilesRemoved)
        assertEquals(CaptureJourneyStatus.IDLE, world.journey.currentSnapshot().status)
    }

    @Test fun capabilitySelectionFailsClosedAndHonoursApprovedFallbacks() = runTest {
        val core = FakeCore(
            FakeCore.requirements(
                FakeCore.requirement(
                    "selfie", "idenqa.evidence.selfie_image", "idenqa.artefact.selfie_image",
                    methods = listOf("idenqa.method.live_camera", "idenqa.method.file_upload"),
                ),
            ),
        )
        val world = makeWorld(
            core,
            declaredMethods = listOf("idenqa.method.file_upload"),
            availableMethods = listOf("idenqa.method.file_upload"),
        )
        val capability = world.journey.getCapabilities()
        assertEquals(listOf("idenqa.method.file_upload"), capability.advertisement().currentlyAvailableMethods)
        assertFalse(capability.advertisement().implementedMethods.any { it.contains("assurance") })
        val snapshot = world.journey.start("idq_cap_v1_fixture")
        assertEquals(listOf("idenqa.method.file_upload"), snapshot.currentTask?.methodOptions)
    }

    @Test fun unsupportedAllOfFailsClosedWithoutInventingFallbacks() = runTest {
        val core = FakeCore(
            FakeCore.requirements(
                FakeCore.requirement(
                    "selfie", "idenqa.evidence.selfie_image", "idenqa.artefact.selfie_image",
                    strategy = "all_of",
                    methods = listOf("idenqa.method.live_camera", "idenqa.method.file_upload"),
                ),
            ),
        )
        val world = makeWorld(
            core,
            declaredMethods = listOf("idenqa.method.file_upload"),
            availableMethods = listOf("idenqa.method.file_upload"),
        )
        assertEquals(IdenqaException.NoCompatibleMethod, runCatching {
            world.journey.start("idq_cap_v1_fixture")
        }.exceptionOrNull())
    }

    @Test fun policyApprovedMethodFallbackIsSelected() = runTest {
        val core = FakeCore(
            FakeCore.requirements(
                FakeCore.requirement(
                    "selfie", "idenqa.evidence.selfie_image", "idenqa.artefact.selfie_image",
                    methods = listOf("vendor.unknown.liveness"),
                    fallbacks = """{"on":["method_unavailable"],"acquisition":{"strategy":"any_of","methods":["idenqa.method.file_upload"]}}""",
                ),
            ),
        )
        val world = makeWorld(core)
        val snapshot = world.journey.start("idq_cap_v1_fixture")
        assertEquals(CaptureFallbackCondition.METHOD_UNAVAILABLE, snapshot.currentTask?.fallbackCondition)
        assertEquals(listOf("idenqa.method.file_upload"), snapshot.currentTask?.methodOptions)
    }

    @Test fun captureFailureAppliesOnlyPolicyApprovedFallback() = runTest {
        val core = FakeCore(
            FakeCore.requirements(
                FakeCore.requirement(
                    "selfie", "idenqa.evidence.selfie_image", "idenqa.artefact.selfie_image",
                    methods = listOf("idenqa.method.live_camera"),
                    fallbacks = """{"on":["capture_failed"],"acquisition":{"strategy":"any_of","methods":["idenqa.method.file_upload"]}}""",
                ),
            ),
        )
        val world = makeWorld(core)
        val started = world.journey.start("idq_cap_v1_fixture")
        val task = assertNotNull(started.currentTask)
        val failed = world.journey.captureFailed(task.id)
        assertEquals(CaptureFallbackCondition.CAPTURE_FAILED, failed.currentTask?.fallbackCondition)
        assertEquals(listOf("idenqa.method.file_upload"), failed.currentTask?.methodOptions)
        assertEquals(CaptureGuidanceCode.QUALITY_REJECTED, failed.guidance.code)

        val missing = FakeCore(
            FakeCore.requirements(
                FakeCore.requirement(
                    "selfie", "idenqa.evidence.selfie_image", "idenqa.artefact.selfie_image",
                    methods = listOf("idenqa.method.live_camera"),
                ),
            ),
        )
        val world2 = makeWorld(missing)
        val started2 = world2.journey.start("idq_cap_v1_fixture")
        val task2 = assertNotNull(started2.currentTask)
        assertEquals(IdenqaException.NoCompatibleMethod, runCatching {
            world2.journey.captureFailed(task2.id)
        }.exceptionOrNull())
    }

    @Test fun lifecycleTransitionsKeepCancellationReachable() = runTest {
        val core = FakeCore()
        val world = makeWorld(core)
        world.journey.start("idq_cap_v1_fixture")
        assertEquals(CaptureGuidanceCode.BACKGROUNDED, world.journey.applicationEnteredBackground().guidance.code)
        assertTrue(world.journey.currentSnapshot().canCancel)
        assertEquals(CaptureGuidanceCode.NONE, world.journey.applicationEnteredForeground().guidance.code)
        assertEquals(CaptureGuidanceCode.INTERRUPTED, world.journey.captureInterruptionBegan().guidance.code)
        assertEquals(CaptureGuidanceCode.NONE, world.journey.captureInterruptionEnded().guidance.code)
        assertEquals(CaptureGuidanceCode.NETWORK_UNAVAILABLE, world.journey.networkBecameUnavailable().guidance.code)
        assertEquals(CaptureGuidanceCode.NONE, world.journey.networkBecameAvailable().guidance.code)
        val denied = world.journey.cameraPermissionChanged(granted = false)
        assertEquals(CaptureGuidanceCode.CAMERA_PERMISSION_DENIED, denied.guidance.code)
        assertEquals(CaptureRecoveryAction.OPEN_SETTINGS, denied.guidance.recovery)
        assertEquals(CaptureGuidanceCode.NONE, world.journey.cameraPermissionChanged(granted = true).guidance.code)
        assertEquals(CaptureGuidanceCode.SECURE_SCREEN_CAPTURED, world.journey.screenCaptureChanged(isCaptured = true).guidance.code)
        assertEquals(CaptureGuidanceCode.NONE, world.journey.screenCaptureChanged(isCaptured = false).guidance.code)
    }

    @Test fun backgroundingDoesNotDuplicateWorkOrLoseTheReference() = runTest {
        val core = FakeCore()
        val world = makeWorld(core)
        world.journey.start("idq_cap_v1_fixture")
        world.journey.applicationEnteredBackground()
        world.journey.applicationEnteredForeground()
        world.journey.applicationEnteredForeground()
        val stored = assertNotNull(world.referenceStore.read())
        assertEquals("ver_01J00000000000000000000000", stored.verificationId)
        assertEquals(0, core.cancelMutations)
        assertTrue(core.uploadBodies.isEmpty())
    }

    @Test fun fileBackedTemporaryStoreEnforcesBoundsAndPurges() = runTest {
        val directory = Files.createTempDirectory("idenqa-tests").toFile()
        try {
            val store = AndroidTemporaryFileStore(File(directory, "install-1"), 1_024, 1_500, "install-1")
            val oversized = CapturedArtifact(ByteArray(2_048) { 5 }, "image/jpeg", "idenqa.method.live_camera")
            assertEquals(IdenqaException.TemporaryStorage, runCatching {
                store.stage(oversized, "evidence", Instant.now())
            }.exceptionOrNull())
            val small = CapturedArtifact(ByteArray(1_024) { 5 }, "image/jpeg", "idenqa.method.live_camera")
            val staged = store.stage(small, "evidence", Instant.now())
            assertEquals(1, store.count())
            assertEquals(1_024, store.totalBytes())
            assertTrue(store.data(staged).contentEquals(small.bytes()))
            assertEquals(IdenqaException.TemporaryStorage, runCatching {
                store.stage(small, "evidence", Instant.now())
            }.exceptionOrNull())
            assertEquals(1, store.purgeAll())
            assertEquals(0, store.count())
        } finally {
            directory.deleteRecursively()
        }
    }

    @Test fun sensitiveScreenProtectionIsAppliedAndRemovable() = runTest {
        val core = FakeCore()
        val protection = FakeScreenProtection()
        val world = makeWorld(core, screenProtection = protection)
        world.journey.start("idq_cap_v1_fixture")
        assertTrue(world.journey.applySensitiveScreenProtection())
        assertTrue(protection.protected)
        protection.captured = true
        assertTrue(world.journey.screenCaptureState())
        world.journey.removeSensitiveScreenProtection()
        assertFalse(protection.protected)
    }

    @Test fun persistedReferenceRoundTripsThroughSecureEncoding() {
        val reference = CaptureJourneyReference(
            verificationId = "ver_1",
            sessionVersion = 3,
            region = "ng-1",
            profileRevision = 1,
            profileDigest = "sha256:${"a".repeat(64)}",
            expiresAt = Instant.ofEpochSecond(1_788_523_200),
            completedTaskIds = listOf("a", "b"),
            failedTaskIds = listOf("c"),
            idempotencyKeys = mapOf("cancel" to "capture_cancel_1", "upload.issue:x" to "capture_upload_2"),
        )
        val encoded = JourneyJson.encodeJourneyReference(reference)
        assertEquals(reference, JourneyJson.decodeJourneyReference(encoded))
        assertFalse(encoded.contains("evidence"))
    }

    @Test fun sharedPublishedFixtureDecodesIntoJourneyDocuments() {
        val root = File(System.getProperty("idenqa.repo.root"), "contracts/capture/native/v1/fixtures")
        val document = JourneyJson.decodeSession(File(root, "bootstrap-response.json").readText())
        assertEquals("ng-1", document.region)
        assertTrue(document.requirements.requirements.isEmpty())
    }
}

private fun assertFailure(expected: IdenqaException, block: () -> Unit) {
    val error = runCatching(block).exceptionOrNull()
    assertEquals(expected, error)
}

