package dev.idenqa.sdk

import java.io.IOException
import java.net.URI
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.coroutines.suspendCancellableCoroutine
import okhttp3.Call
import okhttp3.Callback
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener

data class TransportRequest(val method: String, val uri: URI, val headers: Map<String, String>, val body: ByteArray?)
data class TransportResponse(val status: Int, val body: ByteArray)

interface HttpTransport { suspend fun send(request: TransportRequest): TransportResponse }

class OkHttpTransport(private val client: OkHttpClient = OkHttpClient()) : HttpTransport {
    override suspend fun send(request: TransportRequest): TransportResponse = suspendCancellableCoroutine { continuation ->
        val builder = Request.Builder().url(request.uri.toURL())
        request.headers.forEach(builder::header)
        val body = request.body?.toRequestBody(request.headers["Content-Type"]?.toMediaType())
        builder.method(request.method, body)
        val call = client.newCall(builder.build())
        continuation.invokeOnCancellation { call.cancel() }
        call.enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                if (continuation.isActive) continuation.resumeWithException(e)
            }
            override fun onResponse(call: Call, response: Response) {
                response.use {
                    if (continuation.isActive) continuation.resume(TransportResponse(it.code, it.body.bytes()))
                }
            }
        })
    }
}

interface RealtimeTransport { fun events(uri: URI, ticket: String): Flow<RealtimeEvent> }

class OkHttpRealtimeTransport(private val client: OkHttpClient = OkHttpClient()) : RealtimeTransport {
    override fun events(uri: URI, ticket: String): Flow<RealtimeEvent> = callbackFlow {
        val request = Request.Builder().url(uri.toURL()).header("Authorization", "Bearer $ticket").build()
        val socket = client.newWebSocket(request, object : WebSocketListener() {
            override fun onMessage(webSocket: WebSocket, text: String) { trySend(Json.decodeEvent(text)) }
            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) { close(t) }
            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) { close() }
        })
        awaitClose { socket.cancel() }
    }
}
