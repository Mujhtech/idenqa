package dev.idenqa.sdk

import android.content.Context
import java.io.File
import java.time.Instant

/** Opaque handle to one staged temporary evidence file. */
data class CaptureTemporaryFile(val reference: String, val byteCount: Int, val createdAt: Instant)

/**
 * Bounded, app-private staging storage for in-flight evidence.
 *
 * Implementations must keep files inside app-private storage, enforce the
 * configured bounds, never write evidence bytes to logs, and remove staged
 * files deterministically. Raw evidence is never persisted across process
 * death: staging only covers an in-flight attempt.
 */
interface CaptureTemporaryFileStore {
    suspend fun stage(artifact: CapturedArtifact, category: String, now: Instant): CaptureTemporaryFile
    suspend fun data(file: CaptureTemporaryFile): ByteArray
    suspend fun remove(file: CaptureTemporaryFile)
    suspend fun purge(olderThan: Instant): Int
    suspend fun purgeAll(): Int
    suspend fun count(): Int
    suspend fun totalBytes(): Int
}

/**
 * App-private staging store backed by `Context.noBackupFilesDir`.
 *
 * Internal storage is already app-private; on API 26+ the directory is also
 * excluded from backup and device transfer. Staged files never leave the
 * private container and are deleted on every completion path.
 */
class AndroidTemporaryFileStore(
    private val root: File,
    private val maximumFileBytes: Int,
    private val maximumTotalBytes: Int,
    namespace: String,
) : CaptureTemporaryFileStore {
    constructor(
        context: Context,
        namespace: String,
        maximumFileBytes: Int,
        maximumTotalBytes: Int,
    ) : this(
        File(File(context.applicationContext.noBackupFilesDir, "idenqa-capture"), namespace),
        maximumFileBytes,
        maximumTotalBytes,
        namespace,
    )

    private val lock = Any()

    init {
        if (!CaptureConfiguration.validOpaqueReference(namespace, 128) ||
            maximumFileBytes <= 0 || maximumTotalBytes < maximumFileBytes
        ) throw IdenqaException.InvalidConfiguration
    }

    override suspend fun stage(artifact: CapturedArtifact, category: String, now: Instant): CaptureTemporaryFile = synchronized(lock) {
        if (!CaptureConfiguration.validOpaqueReference(category, 64)) throw IdenqaException.InvalidConfiguration
        if (artifact.contentType != "image/jpeg" && artifact.contentType != "image/png") throw IdenqaException.InvalidConfiguration
        val bytes = artifact.bytes()
        if (bytes.isEmpty() || bytes.size > maximumFileBytes) throw IdenqaException.TemporaryStorage
        ensureDirectory()
        if (totalBytesLocked() + bytes.size > maximumTotalBytes) throw IdenqaException.TemporaryStorage
        val file = File(root, "$category-${java.util.UUID.randomUUID()}.bin")
        try {
            file.outputStream().use { it.write(bytes) }
        } catch (_: Exception) {
            file.delete()
            throw IdenqaException.TemporaryStorage
        } finally {
            bytes.fill(0)
        }
        CaptureTemporaryFile(file.name, artifact.bytes().size, now)
    }

    override suspend fun data(file: CaptureTemporaryFile): ByteArray = synchronized(lock) {
        try {
            resolve(file).readBytes()
        } catch (_: Exception) {
            throw IdenqaException.TemporaryStorage
        }
    }

    override suspend fun remove(file: CaptureTemporaryFile) {
        synchronized(lock) { resolve(file).delete() }
    }

    override suspend fun purge(olderThan: Instant): Int = synchronized(lock) {
        var removed = 0
        root.listFiles()?.forEach { file ->
            if (file.lastModified() < olderThan.toEpochMilli()) {
                if (file.delete()) removed++
            }
        }
        removed
    }

    override suspend fun purgeAll(): Int = synchronized(lock) {
        var removed = 0
        root.listFiles()?.forEach { file -> if (file.delete()) removed++ }
        removed
    }

    override suspend fun count(): Int = synchronized(lock) { root.listFiles()?.size ?: 0 }

    override suspend fun totalBytes(): Int = synchronized(lock) { totalBytesLocked() }

    private fun totalBytesLocked(): Int = root.listFiles()?.sumOf { it.length().toInt() } ?: 0

    private fun ensureDirectory() {
        if (root.isDirectory) return
        if (root.exists() || !root.mkdirs()) throw IdenqaException.TemporaryStorage
    }

    private fun resolve(file: CaptureTemporaryFile): File {
        if (file.reference.isEmpty() || file.reference.contains('/') || file.reference != File(file.reference).name) {
            throw IdenqaException.InvalidConfiguration
        }
        return File(root, file.reference)
    }
}
