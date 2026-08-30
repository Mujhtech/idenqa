# Capture realtime protocol v1

This directory is the public, provider-neutral contract for Idenqa's active
capture control channel. The WebSocket subprotocol is
`idenqa.capture.v1`. `message.schema.json` defines the JSON message envelope
and the closed v1 payload catalogue.

The channel is control-only. Evidence bytes, filenames, integrity digests,
capture tokens, connection tickets, biometric templates, provider secrets,
and unrestricted provider results are forbidden. Evidence continues to use
the dedicated authenticated HTTP upload protocol.

## Connection bootstrap

The capture client calls `POST /v1/capture/connections` using its short-lived
capture token. The server infers the verification from that principal and
returns a display-once WebSocket URL containing a single-use query ticket. A
browser cannot attach an `Authorization` header to the WebSocket constructor.
The full URL and query string are therefore credentials and must be redacted
from application, reverse-proxy, access, analytics, and tracing logs.

Browser issuance requires exactly one configured allowed `Origin`. The public
WebSocket endpoint and regional data-plane identity come from trusted operator
configuration and are not derived from request host or forwarding headers, IP,
locale, or device language. The response uses `Cache-Control: no-store` and is
not idempotently replayed: retrying safely creates a new independent ticket.
An authenticated native-application bootstrap adapter remains a separate
surface because native identity cannot be accepted from an arbitrary header.

The ticket defaults to a 30-second lifetime, configurable from 10 through 60
seconds. It contains a 256-bit random secret; PostgreSQL stores only its
domain-separated SHA-256 digest and a digest-format version. Redemption is
atomic and binds the tenant, verification, capture-token record, exact browser
origin or authenticated native application identity, region, and protocol
version. The ticket is not reusable and never replaces the capture token
outside the WebSocket handshake.

The Go server adapter uses `github.com/coder/websocket` v1.8.15 behind the
owned transport boundary. Idenqa compares the browser Origin exactly before
upgrade instead of using origin patterns, requires the v1 subprotocol offer,
and disables compression. After a valid upgrade, all malformed, missing,
unknown, mismatched, expired, revoked, or reused ticket values enter the same
atomic redemption path and receive the same generic policy-violation close.

## Version and sequence policy

A connection negotiates the exact `idenqa.capture.v1` subprotocol and begins
with `client.hello` followed by `server.welcome`. Additive optional fields may
be introduced within v1. Removing a field, changing its meaning, or changing
required behaviour requires `idenqa.capture.v2`.

Sequences are connection-local and start at one independently in each
direction. `server.event_ack.server_sequence` acknowledges safe server
messages on that connection. A capture-visible durable server message may also
carry `event_cursor`, a verification-local PostgreSQL position that survives
connections and API nodes. `client.hello.last_acknowledged_event_cursor` asks
the new node to replay after that position, and
`server.event_ack.event_cursor` persists the exact capture token's monotonic
durable acknowledgement. The connection-local sequence resets to zero on
reconnect; it is never used as a durable cursor.

Durable payloads are limited to capture commands, challenge request or
cancellation, aggregate capture progress, capture-safe verification-check
state/version progress, and session-state changes. Check progress deliberately
excludes outcome, signals, reason codes, provider data, and evidence. The
event record and its consequential application transition commit atomically.
Replay is ordered and bounded. A cursor ahead of the stream or older than the
retained floor emits `session.resync_required`; the client then reads the
authoritative REST snapshots. Raw evidence is never a durable control event.

PostgreSQL is the only correctness dependency. `LISTEN/NOTIFY` wakes connected
nodes early, but every wake-up causes a replay query and a bounded 500 ms poll
continues when notifications are duplicated, delayed, unavailable, or lost.
No sticky session or Redis coordination is required.

Within one connection, `server.event_ack.server_sequence` is a monotonic,
cumulative acknowledgement through that server sequence. It cannot
acknowledge a sequence that has not been written on the connection. Duplicate
acknowledgement of the current sequence is harmless; regression is a protocol
violation.

`GET /v1/capture/progress` returns a strong opaque ETag. Capture clients may
use `If-None-Match`; a 304 response means the accepted completion projection
has not changed. The public TypeScript SDK exposes conditional progress
polling, and Capture Web uses it only after its bounded WebSocket reconnect
budget is exhausted. The polling interval defaults to two seconds and is
configurable from 250 ms through 30 seconds.

Consequential messages carry a stable `cmd_` command ID. Receivers acknowledge
and deduplicate their effects; delivery is at least once, not exactly once.

## Selected limits

Defaults are 16 KiB per message, an outbound queue of 64, at most 16
unacknowledged commands, 5-second hello and write deadlines, a 15-second ping
interval, 10-second pong deadline, 45-second idle timeout, and a 30-minute
connection lifetime that never exceeds session expiry. Native WebSocket
ping/pong is used and compression is disabled. A successful ping/pong exchange
counts as transport activity, so a healthy socket is not closed merely because
the subject is reading instructions or evidence is moving over HTTP. Idle
expiry closes with retryable WebSocket status 1001 and a static reason.

The server never waits for room in the outbound queue. Exhausting the queue or
the unacknowledged-command allowance classifies the client as a slow consumer,
cancels the connection work, and closes the socket with WebSocket status 1013
and a static non-sensitive reason. A sequence is assigned only after a message
has secured a queue slot.

During process drain, new socket admissions receive HTTP 503 with a bounded
`Retry-After` hint before ticket redemption. An already admitted established
connection receives `server.draining` with the initial five-second reconnect
hint before WebSocket status 1012. The process tracks admitted handlers because
ordinary HTTP graceful shutdown does not own hijacked WebSocket connections.
