# `@idenqa/sdk`

The public, zero-runtime-dependency TypeScript client for self-hosted Idenqa Core. It supports Node.js 22+ and modern browsers through the standard Fetch and Abort APIs. Effect is not required.

```ts
import { CaptureClient, IdenqaClient, createIdempotencyKey } from "@idenqa/sdk";

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

`verification.check.progress` is durable across reconnects and deliberately
contains no outcome, signals, reason codes, evidence, or provider data. Clients
should reread the authoritative REST snapshot when coordinating application
state.

Keep API keys and capture tokens out of URLs, logs, analytics, traces, and error messages. `observe` keeps each display-once WebSocket URL internal and obtains a fresh ticket for every reconnect; callers should not log, persist, analyse, or reproduce it. Store an idempotency key and reuse it only with the same idempotent operation and input. Authority declarations are tenant assertions: Idenqa validates and enforces their exact scope but does not select or certify a lawful basis. Every REST method accepts an optional `AbortSignal`; successful results include the server-issued `requestId`, and conditional resources include their strong `etag` when supplied by the API.
