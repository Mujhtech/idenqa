# `@idenqa/sdk`

The public, zero-runtime-dependency TypeScript client for self-hosted Idenqa Core. It supports Node.js 22+ and modern browsers through the standard Fetch and Abort APIs. Effect is not required.

The 6 September 2026 alpha contract expands `VerificationState` from `collecting`
to the full workflow vocabulary. Update exhaustive state handling when adopting
this change. A `completed` session references a separate identity decision;
`cancelled`, `expired` and `failed` do not mean the subject was not verified.
Creation retries retain the original creation snapshot; use
`idenqa.verifications.get(id)` for current state. The vocabulary does not imply
that every initiating operation is already available.

```ts
import { CaptureClient, IdenqaClient, OutcomeClient, createIdempotencyKey } from "@idenqa/sdk";

const idenqa = new IdenqaClient({
  baseUrl: "https://identity.example.com",
  apiKey: process.env.IDENQA_API_KEY!,
});

const created = await idenqa.captureProfiles.create(
  {
    name: "Selfie upload",
    document: {
      schema_version: 1,
      registry: {
        schema_version: 1,
        revision: 1,
        digest: "sha256:71ef9df77044f9bf5eeb7ae448da3d98811ff4cb3f2541f459e60b4628a5364f",
      },
      requirements: [
        {
          key: "selfie",
          purpose: "idenqa.purpose.identity_verification",
          evidence_type: "idenqa.evidence.selfie_image",
          artefacts: ["idenqa.artefact.selfie_image"],
          acquisition: { strategy: "any_of", methods: ["idenqa.method.file_upload"] },
          required_assurances: [],
          constraints: [],
          fallbacks: [],
        },
      ],
    },
  },
  { idempotencyKey: createIdempotencyKey("profile") },
);

const published = await idenqa.captureProfiles.publish(created.data.profileId, {
  etag: created.etag!,
  idempotencyKey: createIdempotencyKey("publish"),
});

const verificationKey = createIdempotencyKey("verification");
const input = { captureProfileId: published.data.profileId };
const verification = await idenqa.verifications.create(input, {
  idempotencyKey: verificationKey,
});

const notice = await idenqa.notices.create(
  {
    key: "tenant.notice.identity_verification",
    locale: "en-NG",
    controller: "Example Controller",
    recipient: "Example Recipient",
    copy: {
      title: "Identity verification",
      summary: "We need to verify your identity.",
      purpose: "Your evidence is used only for identity verification.",
      consequences: "You may refuse and evidence collection will not continue.",
    },
    effectiveAt: new Date().toISOString(),
  },
  { idempotencyKey: createIdempotencyKey("notice") },
);

await idenqa.authorities.declare(
  verification.data.session.id,
  {
    noticeId: notice.data.id,
    category: "tenant.authority.customer_declared",
    purpose: "idenqa.purpose.identity_verification",
    jurisdiction: "tenant.jurisdiction.ng",
    policyPack: "tenant.policy.identity_v1",
    consentRequired: true,
    recipientReference: "tenant.recipient.primary",
    recipientDisplayName: "Example Recipient",
    regions: ["tenant.region.ng"],
    retentionReference: "tenant.retention.identity_v1",
    validFrom: new Date().toISOString(),
    expiresAt: verification.data.session.expiresAt,
  },
  { idempotencyKey: createIdempotencyKey("authority") },
);

// Retrying the same input with the same key returns the original result.
const replay = await idenqa.verifications.create(input, {
  idempotencyKey: verificationKey,
});

const capture = new CaptureClient({
  baseUrl: "https://identity.example.com",
  captureToken: replay.data.captureToken,
});
const outcomeClient = new OutcomeClient({
  baseUrl: "https://identity.example.com",
  outcomeToken: replay.data.outcomeToken,
});
const snapshot = await capture.getSession();
const processing = await capture.getAuthority();
const progress = await capture.getProgress();
await capture.respond(
  { action: "consent", locale: processing.data.notice.locale },
  { idempotencyKey: createIdempotencyKey("consent") },
);

// Observation owns display-once ticket issuance, strict v1 framing,
// acknowledgement, bounded reconnect, and stable command replay.
const observation = capture.observe(snapshot.data, {
  capabilities: ["idenqa.method.file_upload"],
});
for await (const event of observation) {
  if (event.type === "verification.check.progress") {
    console.log(event.payload.checkId, event.payload.state, event.payload.checkVersion);
  }
  if (event.type === "session.resync_required") {
    await capture.getProgress(); // REST remains authoritative for recovery.
  }
}

// The capture surface supplies this JPEG Blob. Compute and retain the same
// canonical SHA-256 digest with the platform cryptography facility.
declare const image: Blob;
const evidenceDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
const upload = await capture.createEvidenceUpload(
  {
    requirementKey: "selfie",
    artefact: "idenqa.artefact.selfie_image",
    acquisitionMethod: "idenqa.method.file_upload",
    expectedBytes: image.size,
    expectedDigest: evidenceDigest,
    mediaType: "image/jpeg",
    region: "tenant.region.ng",
  },
  { idempotencyKey: createIdempotencyKey("upload") },
);
await capture.uploadEvidence(upload.data.id, image, {
  etag: upload.etag!,
  digest: evidenceDigest,
});

// Once an authoritative decision exists, tenant backends can read the safe
// reproduced report. Export requires the separate decisions:export permission.
declare const decisionId: string;
declare function storeDecisionBundle(canonical: string): Promise<void>;
const decision = await idenqa.decisions.get(decisionId);
const unchanged = await idenqa.decisions.poll(decisionId, decision.etag!);
const portable = await idenqa.decisions.exportBundle(decisionId);
// `canonical` is the exact server representation; `document` is its typed form.
await storeDecisionBundle(portable.data.canonical);
console.log(decision.data.outcome, unchanged.notModified, portable.data.document.bundle_digest);

// After an interrupted or ambiguous PUT, recover the current lifecycle and ETag
// before restarting the complete body.
const currentUpload = await capture.getEvidenceUpload(upload.data.id);
console.log(snapshot.data.requirements, processing.data.notice.copy);
```

Capture surfaces should use the subject-safe outcome projection instead of a
tenant decision report. It uses the separate read-only outcome credential and
remains readable after capture stops, including after session expiry, while that
credential is valid and unrevoked:

```ts
const outcome = await outcomeClient.getOutcome();
console.log(outcome.data.state, outcome.data.sessionVersion);
```

The projection exposes only `capture_required`, `processing`,
`action_required`, `verified`, `not_verified`, `inconclusive`, `cancelled`,
`expired`, or operational `failed`. It deliberately omits policy reasons,
assurance, decision identifiers, provider/model details, evidence metadata, and
subject data.

`verification.check.progress` is durable across reconnects and deliberately
contains no outcome, signals, reason codes, evidence, or provider data. Clients
should reread the authoritative REST snapshot when coordinating application
state.

Keep API keys, capture tokens, and outcome tokens out of URLs, logs, analytics, traces, and error messages. An outcome token grants only `GET /v1/capture/outcome`; it is never accepted for capture, authority response, evidence upload, cancellation, or realtime operations. `observe` keeps each display-once WebSocket URL internal and obtains a fresh ticket for every reconnect; callers should not log, persist, analyse, or reproduce it. Store an idempotency key and reuse it only with the same idempotent operation and input. Authority declarations are tenant assertions: Idenqa validates and enforces their exact scope but does not select or certify a lawful basis. Every REST method accepts an optional `AbortSignal`; successful results include the server-issued `requestId`, and conditional resources include their strong `etag` when supplied by the API.

## Cancel a verification

Tenant backends need `verification_sessions:cancel`. Use the version returned by current session retrieval and preserve both the key and version when retrying:

```ts
const current = await client.verifications.get(verificationId);
const cancelled = await client.verifications.cancel(verificationId, current.data.version, {
  idempotencyKey: "cancel-verification-1",
});
```

Subjects use `capture.cancel(expectedVersion, { idempotencyKey })` on their `CaptureClient`. The token must still be valid and bound to that verification. Exact retries return the original receipt; normal capture operations fail after cancellation. An elapsed deadline or another terminal state rejects a fresh cancellation. Cancellation is a workflow result, separate from identity outcome, consent withdrawal and evidence deletion.

## Manage webhooks from a tenant backend

Use `idenqa.webhooks` with `webhooks:read` for inspection, `webhooks:configure` for endpoint changes and `webhooks:replay` for failed-delivery replay. Existing API keys need explicit replacement to gain newly introduced permissions.

```ts
const endpoint = await idenqa.webhooks.create(
  { url: "https://tenant.example.com/identity-events" },
  { idempotencyKey: "receiver-create-1" },
);
if (endpoint.data.signingSecret !== undefined) {
  await secretStore.put("identity-webhook", endpoint.data.signingSecret);
}
const deliveries = await idenqa.webhooks.listDeliveries(endpoint.data.endpoint.id, { limit: 25 });
const attempts = await idenqa.webhooks.listAttempts(deliveryId);
const replay = await idenqa.webhooks.replay(
  deliveryId,
  { reason: "receiver_recovered" },
  { idempotencyKey: "replay-failed-delivery-1" },
);
```

`list`, `get`, `rotate`, `disable` and `getDelivery` complete the administration surface. Rotation takes `{ expectedVersion, overlapSeconds }`; disablement takes `{ expectedVersion, reason }`. Use the version from `get`. Keep list limits unchanged when passing a returned `page.nextCursor`. Attempt records carry `statusCode`, `errorClass`, `retryAfterMs` and a bounded receiver response excerpt: `responseBody` is at most 4096 bytes sanitised to valid UTF-8, `responseTruncated` reports dropped bytes, and both are untrusted content that must not be parsed or trusted.

Create/rotate return the unpadded Base64URL `signingSecret` only once. Their idempotent retries return the original safe metadata with `replayed: true` and no secret. If the initial secret is lost, perform a new authorised rotation after any active previous-key overlap. Never log the response or put signing material in capture clients. Inspection excludes payloads and keys. Replay is available only for exhausted deliveries on enabled endpoints and preserves the signed event ID and exact body; receiver event-ID deduplication still applies.

Verify the signature over the exact raw body before parsing it. A `verification.completed` payload carries the nested resource snapshot, so a receiver can persist the verification and its checks without a follow-up read:

```ts
type CompletedCheck = { id: string; state: string; outcome?: string };
type VerificationCompleted = {
  id: string;
  type: "verification.completed";
  schema_version: "1.0";
  data: {
    verification_id: string;
    decision_id: string;
    verification: {
      id: string;
      status: string;
      decision_outcome?: string;
      completed_at?: string;
      checks?: CompletedCheck[];
    };
  };
};

const event = JSON.parse(rawBody) as VerificationCompleted;
await storeCompletion(event.data.verification_id, event.data.verification.status);
for (const check of event.data.verification.checks ?? []) {
  await recordCheck(event.data.verification_id, check);
}
```

The envelope is byte-stable across retries and replays; deduplicate on `event.id` instead of assuming one successful attempt. Bodies are KMS-encrypted at rest and delivery inspection never returns raw payloads, so the verified receiver copy is the only plaintext that leaves the boundary.

The equivalent self-hosted CLI calls the same API:

```sh
idenqa webhook create --api-url https://core.example.com \
  --api-key-file ./backend.key --url https://tenant.example.com/identity-events \
  --idempotency-key receiver-create-1 --secret-out ./webhook-secret.key
idenqa webhook deliveries --api-url https://core.example.com \
  --api-key-file ./backend.key --id "$ENDPOINT_ID" --limit 25
idenqa webhook replay --api-url https://core.example.com \
  --api-key-file ./backend.key --id "$DELIVERY_ID" \
  --reason receiver_recovered --idempotency-key replay-failed-delivery-1 --confirm
idenqa webhook listen --api-url https://core.example.com \
  --api-key-file ./backend.key --event-types verification.completed \
  --forward-to http://localhost:4242/webhook --secret-out ./listen-secret.key
```

`webhook listen` streams live catalogue events with `webhooks:read`, prints one summary per event (or the canonical envelope with `--json`), reconnects with `Last-Event-ID`, and with `--forward-to` forwards exact bytes to a loopback receiver signed with the canonical `Idenqa-Signature`, `Idenqa-Timestamp` and `Idenqa-Event-ID` headers. It uses exactly one of `--secret-file` (an existing Base64URL secret) or `--secret-out` (a new owner-only generated secret that persists across restarts); `--print-secret` prints the configured secret and exits. Verify forwards with the Go SDK `WebhookVerifier`. The stream can miss events while the listener is offline; receivers must continue deduplicating by event ID.

`--secret-out` must name a new file; it is created owner-only before the request and never overwritten. Replayed create/rotate responses produce no secret file. Rotation, disablement and replay require `--confirm`. Credentials may alternatively come from `IDENQA_API_KEY`; they are never command-line arguments. Runtime API/file failures return exit status 1, invalid arguments return 2. Replay requires the API and worker to share their configured Headgate installation and schema.

## Manage policies from a tenant backend

Use `idenqa.policies` with `policies:read`, `policies:write` and `policies:activate` as needed. One authorised API key can activate or roll back with version checks and audit. Existing keys require explicit replacement to gain new permissions.

```ts
const definition: PolicyDefinition = {
  schema_major: 1,
  schema_minor: 0,
  verified_assurance: "synthetic.fixture",
  rules: [
    {
      name: "synthetic_success",
      when: 'facts["synthetic.document"] == "satisfied" && facts["synthetic.liveness"] == "satisfied"',
      result: {
        state: "satisfied",
        directive: "complete_verified",
        priority: 1,
        contributing_facts: ["synthetic.document", "synthetic.liveness"],
        reason_codes: [],
      },
    },
  ],
};
await idenqa.policies.validate(definition);
const created = await idenqa.policies.create(definition, { idempotencyKey: "policy-create-1" });
const policyId = created.data.policy.id;
await idenqa.policies.activate(
  policyId,
  { revision: 1, expectedVersion: 0, reason: "tenant_requested" },
  { idempotencyKey: "policy-activate-1" },
);
const source = await idenqa.policies.getRevision(policyId, 1);
const revisions = await idenqa.policies.listRevisions(policyId, { limit: 25 });
```

Import `PolicyDefinition` from `@idenqa/sdk`. Definitions and retrieved `document` keep portable snake_case fields; catalog metadata uses camelCase. This example only accepts synthetic fixture facts and does not establish production assurance. `list`, `get`, `listActivations`, `createRevision` and `rollback` complete the surface. Append with `createRevision(policyId, definition, expectedRevision, { idempotencyKey })`. Rollback takes the same fields as activate and must select a previously activated revision. Read the latest `activationVersion` from `get`; revision numbers and activation versions are distinct. Creation/append never activate implicitly. Keep returned cursors and page limits unchanged.

A session pins policy ID at creation and active revision at decision-snapshot authorship. Activation can affect sessions without a snapshot; existing decisions retain their pinned revision. Retry mutations with the same key and input within the configured retention (24 hours by default) to recover the original metadata with `replayed: true`. Validation has no persistent effects.

The CLI takes a JSON definition file containing the same fields shown above, without a `definition` wrapper or server identity:

```sh
idenqa policy validate --api-url https://core.example.com --api-key-file ./backend.key --file ./policy.json
idenqa policy create --api-url https://core.example.com --api-key-file ./backend.key \
  --file ./policy.json --idempotency-key policy-create-1
idenqa policy activate --api-url https://core.example.com --api-key-file ./backend.key \
  --id "$POLICY_ID" --revision 1 --expected-version 0 --reason tenant_requested \
  --idempotency-key policy-activate-1 --confirm
```

`--file -` reads standard input. `add-revision` requires `--expected-revision`; `get-revision` requires `--revision`. `list`, `get`, `revisions` and `activations` inspect resources. `activate` and `rollback` require `--confirm`, an explicit target, current `--expected-version` and reason. Credentials may come from `IDENQA_API_KEY`; they never appear as command-line arguments. Responses are bounded JSON; errors omit server diagnostic bodies. Public simulation, diff and regression commands remain unavailable.

### Manual review

`client.reviews` exposes the published queue, case, findings, evidence, operator administration,
policy settings, arbitration, correction, appeal and recapture operations. Review request/response
properties use the OpenAPI snake_case names. Mutations that create durable effects take
`{ idempotencyKey }`; case and administration writes use the documented expected version.
Use `reviews:admin` only on a tenant backend to manage tenant-attested operator assignments.

```ts
const page = await client.reviews.listReviewCases({ state: "open", limit: 25 });
const claimed = await client.reviews.claimReviewCase(caseId, { expected_version: version });
const artefacts = await client.reviews.listReviewEvidence(caseId, {
  expected_version: claimed.data.version,
});
```

Keep tenant API keys on the server. A browser may use `createReviewViewer(container)` with a
callback to its authenticated tenant backend returning `{ bytes, expiresAt }` from Core's
single-use display grant. Core returns a redacted, burned-watermark PNG; the viewer clears it
on expiry, visibility loss or blur. It cannot prevent screenshots. Call `destroy()` when leaving
the case. Evidence bytes and capture credentials must not enter logs, browser storage or URLs.

### Tenant fraud and risk

`client.fraud` exposes versioned configuration, exact revision reads, evidence-backed token ingestion, safe receipt reads and non-authoritative hypotheses. Configuration needs `fraud:configure`; ingestion needs `fraud:write` and an explicitly configured source binding. Read operations need `fraud:read`. Use these from the tenant backend. Missing inputs remain inconclusive, and no operation independently verifies or rejects a subject. See [the fraud operating contract](../../docs/tenant-fraud-risk-v0.1.md).

## Persistent subjects and identity records

Use `client.identity` from a tenant backend with the relevant `subjects:*` and `identity:*` scopes. Existing keys must be reissued or rotated to gain these newly registered permissions. Capture credentials cannot administer or reveal identity records.

```ts
const created = await client.identity.createSubject("customer-123", {
  idempotencyKey: "customer-123-create",
});
const subject = created.data.subject!;
const linked = await client.identity.linkVerification(subject.id, verificationId, subject.version, {
  idempotencyKey: "customer-123-link",
});
const records = await client.identity.listRecords(subject.id, { current: true });
```

`addRecord` appends a typed observation or derives an immutable fact, claim or identifier from existing record IDs; it requires the current subject version and returns the incremented version. Corrections use `supersedes`; `rebuildProjection` reconstructs current heads without changing history. `getRecord` and `listRecords` return metadata and identifier masks by default. Full values require `{ reveal: true }`, a separate `identity:reveal` scope and an audited read. Value scalars, including numbers, use canonical strings. Sensitive inputs and reveals must not be logged.

Use `configure`, `getConfiguration`, `getConfigurationRevision` and `getReceipt` to manage and inspect versioned identity corroboration requirements. Tenant assertions remain tenant attested; trusted imports reference completed Core check observations. Distinct IDs or transformations of the same source never establish independent corroboration. `lookupIdentifier` and `lookupExternalReference` can return multiple subjects and never merge them.

`deleteSubject(subjectId, expectedVersion, options)` returns a durable deletion ID and `deleting` subject state. It covers structured values and linked verification evidence, honours legal holds, and waits for backup expiry and deletion proof before the subject becomes `deleted`. The initial atomic deletion cap is 256 linked verifications and 255 evidence objects; requests exceeding coverage fail without partial effects. See [the identity API conventions](../../contracts/api/openapi/v1/conventions.md#persistent-subjects-and-identity-records) for the exact value, provenance, permission and retention contracts.

## Assurance profiles

The assurance surface publishes and validates immutable profile revisions, discovers capability meanings, selects requirements for future policy sessions, and reads immutable session pins. The TypeScript client exposes these operations through `client.assurance`. Decision reports expose requested and achieved dimensions in `typed_assurance` (SDK `typedAssurance`); detailed source references remain in authorised exports. See [assurance profiles and decision context](../../docs/assurance-profiles-v0.1.md) for permissions, idempotency, freshness, correlation and provenance limits.
