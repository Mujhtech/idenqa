# Managed preview journey v0.1

**Status — 17 October 2026:** Implementation of the existing signed sandbox-preview boundary, with local synthetic Core/Cloud journey evidence. This does not select production launch authority, accept a provider/model, or close human Capture acceptance.

## 1. Regional preparation

Creating a signed managed session now requires `IDENQA_MANAGED_LAUNCH_PREPARATION_FILE`. The closed configuration contains one to sixty-four exact tenant/profile/policy/locale routes to pre-existing tenant notice versions and declaration references. See [the example](../deploy/managed-launch-preparation.example.json). Replace every example identifier and declaration reference with the tenant's approved configuration. Core snapshots this operator-installed configuration at startup; it is not a browser-supplied declaration or dynamic administration API.

Core verifies the existing signed workload and deployment binding, validates the configured tenant notice, and creates the pinned session. It then binds processing authority before returning either subject bearer. The immutable notice supplies controller, recipient and disclosure copy. Consent is required; only the subject's separately authenticated response permits collection. No default legal copy, automatic consent, tenant API-key impersonation, or new Console credential permission is introduced.

The preparation uses immutable session times and the original command for durable retry. A failure after session creation withholds the bearer response; an exact retry completes the same session and authority. Changed declaration or delegated actor cannot reuse the original idempotency claim. Existing authority withdrawal and capture expiry still deny processing.

## 2. Storage and runtime grants

Apply schema **91** before running the new API and reconcile the runtime role with `idenqa migrate permissions --runtime-role <role>`. The runtime receives only the session-creation audit columns needed to verify workload and delegated operator ownership, under mandatory tenant predicates and forced RLS. The declaration transaction locks the session and checks its immutable creation actor before attaching authority, subject, audit, outbox and webhook intent.

API-key declaration and session-audit identities retain tenant-scoped foreign keys through generated API-key-only columns. Workload identity remains a separate class. The workload delegated actor must be non-null. Downgrading schema 91 fails if workload declarations remain; audit identities must never be rewritten to make a downgrade succeed.

## 3. Direct browser capture

Core's CORS allowlist now includes the required `Content-Digest` upload header. Exact approved origins and cookie-free capture remain enforced. An upload through a same-origin proxy could previously pass while a real cross-origin browser upload failed its preflight; a dedicated regression test covers both allowed and denied origins.

The Cloud subject page consumes the public Capture package and SDK. It advertises only methods its host can perform, follows profile-approved fallbacks and reads the separately authenticated authoritative outcome. The current host has file upload and document-camera support; active selfie/liveness and NFC require their respective reviewed host integrations. Missing adapters never establish assurance or authorize an unapproved file fallback.

## 4. Local verification and remaining acceptance

The opt-in `integration && cloudjourney` proof starts a real regional API, encrypted evidence storage, restricted PostgreSQL role and Headgate worker, then invokes Cloud's real launch store, transport, built Console and built Capture page. Desktop and mobile Chromium exercise a published upload-only synthetic flow, notice/consent, direct upload, authoritative outcome, Console timeline and signed webhook delivery. The worker is replaced after a failed webhook attempt, and the receiver verifies signatures, rejects tampering and checks stable retry identity/body. The workforce/deployment catalogue and normalized check results are explicit fixtures. No upstream vendor account or production biometric result is exercised.

From the Core repository, after building Cloud's assets:

```sh
IDENQA_CLOUD_JOURNEY_REPOSITORY=/path/to/idenqa-cloud \
  go test -v -tags='integration cloudjourney' ./test/integration \
  -run '^TestCloudVerificationBrowserJourney$' -count=1 -timeout=4m
```

Set `DATABASE_TEST_URL` to a local test administrator URL. The proof creates and removes isolated databases, schemas and runtime roles. Its synthetic credentials remain in owner-only temporary files; browser traces are disabled. Ordinary Core tests and self-hosted use do not require Cloud.

Official provider-sandbox execution, reviewed model results, advanced acquisition/device validation, production key/edge configuration, named human review and release approval remain open. The [Cloud journey report](../../idenqa-cloud/docs/idenqa-cloud-verification-journey-v0.1.md) records the Console and hosting changes. The historical v0.5 architecture is unchanged.
