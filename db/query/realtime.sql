-- name: CreateWebSocketConnectionTicket :execrows
WITH active_authority AS MATERIALIZED (
    SELECT tokens.id
    FROM idenqa.capture_tokens AS tokens
    JOIN idenqa.verification_sessions AS sessions
      ON sessions.tenant_id = tokens.tenant_id
     AND sessions.id = tokens.verification_id
    WHERE tokens.tenant_id = sqlc.arg(tenant_id)
      AND tokens.id = sqlc.arg(capture_token_id)
      AND tokens.verification_id = sqlc.arg(verification_id)
      AND tokens.revoked_at IS NULL
      AND tokens.issued_at <= sqlc.arg(issued_at)
      AND tokens.expires_at >= sqlc.arg(expires_at)
      AND sessions.state = 'collecting'
      AND sessions.created_at <= sqlc.arg(issued_at)
      AND sessions.expires_at >= sqlc.arg(expires_at)
    FOR SHARE OF tokens, sessions
)
INSERT INTO idenqa.websocket_connection_tickets (
    id, tenant_id, verification_id, capture_token_id, digest, digest_version,
    client_kind, client_identity, region, protocol, issued_at, expires_at,
    redeemed_at, connection_id
)
SELECT
    sqlc.arg(id), sqlc.arg(tenant_id), sqlc.arg(verification_id),
    sqlc.arg(capture_token_id), sqlc.arg(digest), 1,
    sqlc.arg(client_kind), sqlc.arg(client_identity), sqlc.arg(region),
    sqlc.arg(protocol), sqlc.arg(issued_at), sqlc.arg(expires_at), NULL, NULL
FROM active_authority;

-- name: RedeemWebSocketConnectionTicket :one
WITH active_authority AS MATERIALIZED (
    SELECT tokens.id
    FROM idenqa.capture_tokens AS tokens
    JOIN idenqa.verification_sessions AS sessions
      ON sessions.tenant_id = tokens.tenant_id
     AND sessions.id = tokens.verification_id
    JOIN idenqa.websocket_connection_tickets AS bound_ticket
      ON bound_ticket.tenant_id = tokens.tenant_id
     AND bound_ticket.capture_token_id = tokens.id
     AND bound_ticket.verification_id = sessions.id
    WHERE bound_ticket.tenant_id = sqlc.arg(tenant_id)
      AND bound_ticket.id = sqlc.arg(id)
      AND tokens.revoked_at IS NULL
      AND tokens.issued_at <= sqlc.arg(redeemed_at)
      AND tokens.expires_at > sqlc.arg(redeemed_at)
      AND sessions.state = 'collecting'
      AND sessions.created_at <= sqlc.arg(redeemed_at)
      AND sessions.expires_at > sqlc.arg(redeemed_at)
    FOR SHARE OF tokens, sessions
)
UPDATE idenqa.websocket_connection_tickets AS tickets
SET redeemed_at = sqlc.arg(redeemed_at),
    connection_id = sqlc.arg(connection_id)
WHERE tickets.tenant_id = sqlc.arg(tenant_id)
  AND tickets.id = sqlc.arg(id)
  AND tickets.digest = sqlc.arg(digest)
  AND tickets.digest_version = 1
  AND tickets.client_kind = sqlc.arg(client_kind)
  AND tickets.client_identity = sqlc.arg(client_identity)
  AND tickets.region = sqlc.arg(region)
  AND tickets.protocol = sqlc.arg(protocol)
  AND tickets.redeemed_at IS NULL
  AND tickets.connection_id IS NULL
  AND tickets.issued_at <= sqlc.arg(redeemed_at)
  AND tickets.expires_at > sqlc.arg(redeemed_at)
  AND EXISTS (SELECT 1 FROM active_authority)
RETURNING *;

-- name: LoadRealtimeSessionAuthority :one
SELECT
    sessions.version AS session_version,
    sessions.expires_at AS session_expires_at
FROM idenqa.websocket_connection_tickets AS tickets
JOIN idenqa.capture_tokens AS tokens
  ON tokens.tenant_id = tickets.tenant_id
 AND tokens.id = tickets.capture_token_id
 AND tokens.verification_id = tickets.verification_id
JOIN idenqa.verification_sessions AS sessions
  ON sessions.tenant_id = tickets.tenant_id
 AND sessions.id = tickets.verification_id
WHERE tickets.tenant_id = sqlc.arg(tenant_id)
  AND tickets.id = sqlc.arg(ticket_id)
  AND tickets.verification_id = sqlc.arg(verification_id)
  AND tickets.capture_token_id = sqlc.arg(capture_token_id)
  AND tickets.connection_id = sqlc.arg(connection_id)
  AND tickets.redeemed_at IS NOT NULL
  AND sessions.state = 'collecting'
  AND sessions.created_at <= sqlc.arg(observed_at)
  AND sessions.expires_at > sqlc.arg(observed_at)
  AND tokens.revoked_at IS NULL
  AND tokens.issued_at <= sqlc.arg(observed_at)
  AND tokens.expires_at > sqlc.arg(observed_at);

-- name: FindRealtimeClientCommand :one
SELECT *
FROM idenqa.realtime_client_commands
WHERE tenant_id = sqlc.arg(tenant_id)
  AND command_id = sqlc.arg(command_id);

-- name: LoadRealtimeCommandAuthority :one
SELECT sessions.requirements, sessions.document_selections
FROM idenqa.websocket_connection_tickets AS tickets
JOIN idenqa.capture_tokens AS tokens
  ON tokens.tenant_id = tickets.tenant_id
 AND tokens.id = tickets.capture_token_id
 AND tokens.verification_id = tickets.verification_id
JOIN idenqa.verification_sessions AS sessions
  ON sessions.tenant_id = tickets.tenant_id
 AND sessions.id = tickets.verification_id
WHERE tickets.tenant_id = sqlc.arg(tenant_id)
  AND tickets.id = sqlc.arg(ticket_id)
  AND tickets.verification_id = sqlc.arg(verification_id)
  AND tickets.capture_token_id = sqlc.arg(capture_token_id)
  AND tickets.connection_id = sqlc.arg(connection_id)
  AND tickets.redeemed_at IS NOT NULL
  AND sessions.state = 'collecting'
  AND sessions.created_at <= sqlc.arg(observed_at)
  AND sessions.expires_at > sqlc.arg(observed_at)
  AND tokens.revoked_at IS NULL
  AND tokens.issued_at <= sqlc.arg(observed_at)
  AND tokens.expires_at > sqlc.arg(observed_at)
FOR SHARE OF sessions, tokens;

-- name: InsertRealtimeClientCommand :execrows
INSERT INTO idenqa.realtime_client_commands (
    command_id, tenant_id, verification_id, capture_token_id,
    request_fingerprint, message_type, step_state, requirement_key,
    artefact, acquisition_method, result_code, disposition, rejection_code,
    occurred_at, received_at
) VALUES (
    sqlc.arg(command_id), sqlc.arg(tenant_id), sqlc.arg(verification_id),
    sqlc.arg(capture_token_id), sqlc.arg(request_fingerprint),
    sqlc.arg(message_type), sqlc.arg(step_state), sqlc.arg(requirement_key),
    sqlc.arg(artefact), sqlc.arg(acquisition_method), sqlc.narg(result_code),
    sqlc.arg(disposition), sqlc.narg(rejection_code),
    sqlc.arg(occurred_at), sqlc.arg(received_at)
)
ON CONFLICT (command_id) DO NOTHING;

-- name: DeleteExpiredUnredeemedWebSocketTickets :many
WITH expired AS MATERIALIZED (
    SELECT candidate.id
    FROM idenqa.websocket_connection_tickets AS candidate
    WHERE candidate.tenant_id = sqlc.arg(tenant_id)
      AND candidate.redeemed_at IS NULL
      AND candidate.expires_at <= sqlc.arg(observed_at)
    ORDER BY candidate.expires_at, candidate.id
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE SKIP LOCKED
)
DELETE FROM idenqa.websocket_connection_tickets AS tickets
USING expired
WHERE tickets.tenant_id = sqlc.arg(tenant_id)
  AND tickets.id = expired.id
RETURNING tickets.id;

-- name: EnsureRealtimeStream :execrows
INSERT INTO idenqa.realtime_streams (
    tenant_id, verification_id, latest_cursor, latest_expires_at, retained_from_cursor, updated_at
) VALUES (
    sqlc.arg(tenant_id), sqlc.arg(verification_id), 0, NULL, 1, sqlc.arg(updated_at)
)
ON CONFLICT (tenant_id, verification_id) DO NOTHING;

-- name: LockRealtimeStream :one
SELECT latest_cursor, latest_expires_at, retained_from_cursor
FROM idenqa.realtime_streams
WHERE tenant_id = sqlc.arg(tenant_id)
  AND verification_id = sqlc.arg(verification_id)
FOR UPDATE;

-- name: FindRealtimeEventByID :one
SELECT *
FROM idenqa.realtime_events
WHERE tenant_id = sqlc.arg(tenant_id)
  AND event_id = sqlc.arg(event_id);

-- name: AdvanceRealtimeStream :one
UPDATE idenqa.realtime_streams
SET latest_cursor = latest_cursor + 1,
    latest_expires_at = sqlc.arg(expires_at),
    updated_at = sqlc.arg(updated_at)
WHERE tenant_id = sqlc.arg(tenant_id)
  AND verification_id = sqlc.arg(verification_id)
RETURNING latest_cursor;

-- name: InsertRealtimeEvent :exec
INSERT INTO idenqa.realtime_events (
    event_id, tenant_id, verification_id, cursor, message_type, command_id,
    correlation_id, causation_id, payload, occurred_at, expires_at
) VALUES (
    sqlc.arg(event_id), sqlc.arg(tenant_id), sqlc.arg(verification_id),
    sqlc.arg(cursor), sqlc.arg(message_type), sqlc.narg(command_id),
    sqlc.narg(correlation_id), sqlc.narg(causation_id), sqlc.arg(payload),
    sqlc.arg(occurred_at), sqlc.arg(expires_at)
);

-- name: NotifyRealtimeEvent :exec
SELECT pg_notify(
    'idenqa_realtime_v1',
    sqlc.arg(tenant_id)::text || ':' || sqlc.arg(verification_id)::text
);

-- name: LoadRealtimeReplayAuthority :one
SELECT
    COALESCE(streams.latest_cursor, 0)::bigint AS latest_cursor,
    CASE
        WHEN streams.latest_cursor IS NULL THEN 1
        ELSE GREATEST(
            streams.retained_from_cursor,
            COALESCE(
                (
                    SELECT MIN(events.cursor)
                    FROM idenqa.realtime_events AS events
                    WHERE events.tenant_id = tickets.tenant_id
                      AND events.verification_id = tickets.verification_id
                      AND events.expires_at > sqlc.arg(observed_at)
                ),
                streams.latest_cursor + 1
            )
        )
    END::bigint AS retained_from_cursor,
    COALESCE(acknowledgements.acknowledged_cursor, 0)::bigint AS acknowledged_cursor
FROM idenqa.websocket_connection_tickets AS tickets
JOIN idenqa.capture_tokens AS tokens
  ON tokens.tenant_id = tickets.tenant_id
 AND tokens.id = tickets.capture_token_id
 AND tokens.verification_id = tickets.verification_id
JOIN idenqa.verification_sessions AS sessions
  ON sessions.tenant_id = tickets.tenant_id
 AND sessions.id = tickets.verification_id
LEFT JOIN idenqa.realtime_streams AS streams
  ON streams.tenant_id = tickets.tenant_id
 AND streams.verification_id = tickets.verification_id
LEFT JOIN idenqa.realtime_acknowledgements AS acknowledgements
  ON acknowledgements.tenant_id = tickets.tenant_id
 AND acknowledgements.verification_id = tickets.verification_id
 AND acknowledgements.capture_token_id = tickets.capture_token_id
WHERE tickets.tenant_id = sqlc.arg(tenant_id)
  AND tickets.id = sqlc.arg(ticket_id)
  AND tickets.verification_id = sqlc.arg(verification_id)
  AND tickets.capture_token_id = sqlc.arg(capture_token_id)
  AND tickets.connection_id = sqlc.arg(connection_id)
  AND tickets.redeemed_at IS NOT NULL
  AND sessions.created_at <= sqlc.arg(observed_at)
  AND sessions.expires_at > sqlc.arg(observed_at)
  AND tokens.revoked_at IS NULL
  AND tokens.issued_at <= sqlc.arg(observed_at)
  AND tokens.expires_at > sqlc.arg(observed_at);

-- name: ListRealtimeEvents :many
SELECT *
FROM idenqa.realtime_events
WHERE tenant_id = sqlc.arg(tenant_id)
  AND verification_id = sqlc.arg(verification_id)
  AND cursor > sqlc.arg(after_cursor)
  AND expires_at > sqlc.arg(observed_at)
ORDER BY cursor
LIMIT sqlc.arg(page_size);

-- name: UpsertRealtimeAcknowledgement :execrows
INSERT INTO idenqa.realtime_acknowledgements (
    tenant_id, verification_id, capture_token_id, acknowledged_cursor, updated_at
) VALUES (
    sqlc.arg(tenant_id), sqlc.arg(verification_id), sqlc.arg(capture_token_id),
    sqlc.arg(acknowledged_cursor), sqlc.arg(updated_at)
)
ON CONFLICT (tenant_id, verification_id, capture_token_id) DO UPDATE
SET acknowledged_cursor = GREATEST(
        idenqa.realtime_acknowledgements.acknowledged_cursor,
        EXCLUDED.acknowledged_cursor
    ),
    updated_at = CASE
        WHEN EXCLUDED.acknowledged_cursor > idenqa.realtime_acknowledgements.acknowledged_cursor
            THEN EXCLUDED.updated_at
        ELSE idenqa.realtime_acknowledgements.updated_at
    END;

-- name: DeleteExpiredRealtimeEvents :many
WITH expired AS MATERIALIZED (
    SELECT events.tenant_id, events.verification_id, events.cursor
    FROM idenqa.realtime_events AS events
    WHERE events.tenant_id = sqlc.arg(tenant_id)
      AND events.expires_at <= sqlc.arg(observed_at)
    ORDER BY events.expires_at, events.verification_id, events.cursor
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE SKIP LOCKED
)
DELETE FROM idenqa.realtime_events AS events
USING expired
WHERE events.tenant_id = expired.tenant_id
  AND events.verification_id = expired.verification_id
  AND events.cursor = expired.cursor
RETURNING events.verification_id;

-- name: RefreshRealtimeRetainedFrom :execrows
UPDATE idenqa.realtime_streams AS streams
SET retained_from_cursor = COALESCE(
        (
            SELECT MIN(events.cursor)
            FROM idenqa.realtime_events AS events
            WHERE events.tenant_id = streams.tenant_id
              AND events.verification_id = streams.verification_id
        ),
        streams.latest_cursor + 1
    ),
    updated_at = sqlc.arg(updated_at)
WHERE streams.tenant_id = sqlc.arg(tenant_id)
  AND streams.verification_id = sqlc.arg(verification_id);
