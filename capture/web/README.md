# Idenqa Web capture

## Measured liveness

Active liveness now gates each challenge on local face measurements rather than
a delay. A stable neutral face precedes each movement; the requested pose must
reach its target and hold before its frame is accepted. The segmented ring shows
measured movement/hold progress. Missing/multiple faces, poor framing, wrong
movement, stale frames and failed configured quality checks pause/reset progress.
Timeout permits another attempt; cancellation releases camera and worker.

The default tracker runs MediaPipe Tasks Vision 1.0.1 in a dedicated classic
worker. Runtime model/WASM assets must be hosted on the page origin:

```sh
corepack pnpm --filter @idenqa/capture build
corepack pnpm --filter @idenqa/capture assets:prepare
```

The second command prepares `demo/public/idenqa-liveness/`; pass a destination
directory as its first argument for another host, and optionally a local model
file as the second argument for offline preparation. The model digest is checked.
Serve these assets at `/idenqa-liveness/` or supply `poseAssetBaseUrl`. Permit
`worker-src 'self'`, same-origin asset fetches and WASM compilation
(`script-src 'self' 'wasm-unsafe-eval'`) in the host/worker CSP. No public CDN or
Google service receives camera frames. The worker bundle is separate from the
main library. See [third-party notices](THIRD_PARTY_NOTICES.md).

Acquisition schema 1.1 adds bounded pose policies; legacy 1.0 plans use the same
non-bypassable defaults. `settleDurationMs` is deprecated and cannot skip checks.
`poseTrackerFactory` is an integration/test seam with subject-right-positive yaw
and up-positive pitch in degrees; a mirrored preview does not change these axes.
Required image-quality metrics still need an `assess` implementation; defaults
measure dimensions, bytes and tracked face count. Local pose checks are not PAD.

**Acceptance open:** real-camera accuracy and left/right behaviour, mobile/browser
performance, representative subject evaluation, calibrated thresholds and explicit
visual/interaction acceptance. Synthetic pose tests establish orchestration only.
The hosted fixture's local acquisition plan is not production plan issuance.
Earlier live-Core timed-liveness evidence does not prove measured pose compliance.

`@idenqa/capture` is the open-source, framework-neutral Web capture package for
Idenqa Core. It consumes only public `@idenqa/sdk` contracts and does not require
Console or managed Cloud.

> **Implementation status:** the package now contains the E-04 mobile-first
> guided journey and safe-default visual system. `demo/hosted.html` and
> `demo/embedded.html` use a server-side local bootstrap against a running Core;
> `demo/index.html` and `demo/react.html` remain development fixtures. Hosted and
> embedded live-Core journeys, responsive and accessibility checks, and visual
> captures pass. The user accepted the visual and interaction direction on
> 14 September 2026. The user then clarified that advanced acquisition should
> be implemented now rather than recorded only as follow-on work. Ordered
> active-liveness challenge orchestration and the fail-closed adapter boundary
> for namespaced acquisition methods are now implemented and covered by unit and
> browser tests. The hosted, embedded, and active-liveness demonstrations now
> continue through real Core processing to a verified terminal decision. The
> real-Core matrix also proves action required, not verified, inconclusive,
> subject cancellation, operational failure, and authoritative expiry. E-04
> remains in review only for explicit advanced-interaction acceptance.
> A live-camera still never implies liveness assurance; production PAD remains
> dependent on an authoritative provider or model result.

The first implementation slice provides the fail-closed capture requirement
planner. It intersects the immutable session snapshot with methods the current
integration implements and the device can actually use, preserves tenant
ordering, expands `all_of` method legs, keeps every required artefact, and
selects only a policy-approved fallback for the actual unavailability reason.

The package includes an explicitly registered Lit Web Component and a
capture-flow controller. The component's programmatic `start` method uses the
public SDK to retrieve the immutable session and exact authority notice, records
the required acknowledgement, consent, or refusal with a retry-stable
idempotency key, and reveals capture methods only after the response permits
collection. Capture tokens are accepted only as programmatic start input; they
are never attributes, rendered state, event data, or logs.

```ts
import { defineIdenqaCapture } from "@idenqa/capture";

defineIdenqaCapture();

const capture = document.querySelector("idenqa-capture");
if (capture === null) throw new Error("Capture element is missing.");

await capture.start({
  baseUrl: "https://idenqa.example.test/",
  captureToken,
  outcomeToken,
  capabilities: {
    supportedMethods: ["idenqa.method.file_upload"],
    availableMethods: ["idenqa.method.file_upload"],
  },
  messageCatalogue: {
    fr: {
      chooseFile: "Choisir un fichier",
    },
  },
});
```

The `captureToken` and separate read-only `outcomeToken` should come from the
tenant's trusted bootstrap flow. Do not place either credential in HTML, a query
string, persistent browser storage, analytics, or logs. Capture Web uses the
outcome token only for the subject-safe outcome projection; it never uses it for
capture, authority response, upload, cancellation, or realtime operations.
Integrated file-upload steps use a labelled native picker, apply the profile's
JPEG/PNG and byte constraints, check the file signature, compute its SHA-256
digest with Web Crypto, require an explicit preview-and-confirm action, and
upload the raw `Blob` through a requirement-bound intent. Filenames are not sent.
Interrupted attempts recover the current upload ETag before restarting from byte
zero.

Integrated live-camera steps request only the camera facing the required
artefact, wait for a usable preview frame, let the subject review or retake the
photo, and upload it with the distinct `idenqa.method.live_camera` binding. The
camera and temporary object URL are released on acceptance, retake,
cancellation, restart, or component detachment. A camera failure activates a
file-upload alternative only when the immutable profile explicitly permits a
`capture_failed` fallback; cancelling the camera does not activate that
fallback.

## Document capture and selection

Document-camera steps use a dark, focused viewfinder with side identification,
expandable help, and a separate review state. The subject confirms **Use Photo**
or chooses **Retake Photo** before an upload begins. Automatic detection and the
manual shutter share the same review flow.

Document choices come from the tenant's published capture profile, pinned into
the session. Add `document_options` to a document-image requirement when creating
the profile through the tenant API:

```ts
const documentRequirement = {
  key: "identity_document",
  purpose: "idenqa.purpose.identity_verification",
  evidence_type: "idenqa.evidence.document_image",
  artefacts: ["idenqa.artefact.document_front", "idenqa.artefact.document_back"],
  document_options: [
    {
      id: "driver_license",
      label: "driver license",
      artefacts: ["idenqa.artefact.document_front", "idenqa.artefact.document_back"],
    },
    { id: "passport", label: "passport", artefacts: ["idenqa.artefact.document_front"] },
  ],
  acquisition: { strategy: "any_of", methods: ["idenqa.method.live_camera"] },
  required_assurances: [],
  constraints: [],
  fallbacks: [],
};
```

Labels should be localised document names suitable for use in a sentence. The
selection applies across that requirement's capture, review, retakes, and recovery.
Capture Web calls `CaptureClient.selectDocument` with the session version and an
idempotency key, then refreshes Core before presenting the selected branch. The
`idenqa:document-selected` event supplies `requirementKey` and `documentType`
after Core confirms. Reload restores the choice from `session.documentSelections`;
hosts do not pass a catalogue or selected ID into `start`.
When the profile offers only one option, Capture Web records it through the same
Core command after the notice response and omits the redundant choice screen.

The requirement's artefacts are the union of its pinned alternatives. Selecting
passport activates its front-only branch; selecting driver license requires
both sides. This does not rewrite the immutable profile snapshot or alter
acquisition methods or assurance. Core rejects uploads before selection and
outside the selected branch. Selection cannot switch once an upload intent
exists for the requirement, including failed or abandoned uploads. Profiles
without `document_options` retain their original fixed-artefact behaviour and
generic identity-document instructions.

The live-Core hosted and embedded fixtures expose this journey at
`/hosted.html?journey=document` and `/embedded.html?journey=document`. The server
creates the profile with driver license, national identity card, and passport
branches before session creation. Core canonicalises the artefact set;
Capture Web presents the front before the back without changing either binding.

The real-Core browser suite recreates the component after the front is accepted,
recovers the document selection and accepted progress from Core, captures the
back, and waits for an authoritative synthetic policy outcome. A passport
journey verifies completion after the front only.
Camera images and the synthetic worker are conformance fixtures, not document
authenticity or biometric-assurance evidence.

### Theme contrast diagnostics

`auditCaptureThemeContrast(palette)` checks resolved opaque sRGB palette values
for normal text, secondary text, button labels, hover labels, and offset focus
rings. Supply `background`, `surface`, `surfaceStrong`, `text`, `muted`, `accent`,
`accentStrong`, and `accentForeground` from your final light or dark theme.
The function returns per-pair diagnostics with the measured ratio and required
minimum (4.5:1 for text, 3:1 for the focus ring); an empty list means those pairs
passed. `captureContrastRatio(foreground, background)` exposes the same unrounded
WCAG 2.x relative-luminance calculation for other rendered pairs.

Hex colours and opaque `rgb(r, g, b)` computed values are supported. Transparency,
unresolved variables and other colour spaces return an unsupported-colour
diagnostic rather than passing. Resolve/composite those colours before auditing.
This is an authoring and conformance tool; it does not alter tenant branding,
enforce Core publication, or certify the accessibility of an entire page.
Browser tests check computed light/dark defaults, low-contrast host overrides,
and the document review action. Portable theme colours live in the shadow
cascade, beneath host stylesheet and inline overrides, and are cleared when a
new journey has no portable theme.

## Styling and theming

`<idenqa-capture>` exposes a public CSS custom-property surface on the host
element. A tenant host can theme the open Shadow DOM without reaching into
package-owned selectors:

```css
idenqa-capture {
  --idq-capture-accent: #5b35d5;
  --idq-capture-accent-strong: #4324ad;
  --idq-capture-accent-foreground: #ffffff;
  --idq-capture-font-family: "Inter", sans-serif;
  --idq-capture-shell-radius: 1rem;
  --idq-capture-control-radius: 0.75rem;
  --idq-capture-liveness-color: #5b35d5;
}
```

The supported appearance variables are:

| Variable                                    | Safe default               | Controls                                     |
| ------------------------------------------- | -------------------------- | -------------------------------------------- |
| `--idq-capture-accent`                      | `#0b6b57`                  | Primary actions, progress, and focus colour  |
| `--idq-capture-accent-strong`               | `#075345`                  | Hover and emphasized accent colour           |
| `--idq-capture-accent-foreground`           | `#ffffff`                  | Content placed on the accent colour          |
| `--idq-capture-background`                  | `#ffffff`                  | Main background and neutral controls         |
| `--idq-capture-surface`                     | `#f3f7f5`                  | Cards and capture panels                     |
| `--idq-capture-surface-strong`              | `#e4f2ed`                  | Icons, tips, and stronger secondary surfaces |
| `--idq-capture-text`                        | `#14201d`                  | Primary text                                 |
| `--idq-capture-muted`                       | `#52605c`                  | Secondary text                               |
| `--idq-capture-border`                      | `#d6dedb`                  | Borders and dividers                         |
| `--idq-capture-error`                       | `#b42318`                  | Error text                                   |
| `--idq-capture-font-family`                 | System sans-serif stack    | Package-owned interface typography           |
| `--idq-capture-shell-max-width`             | `42rem`                    | Maximum component width                      |
| `--idq-capture-shell-min-height`            | Responsive `42rem` minimum | Minimum journey height                       |
| `--idq-capture-shell-padding`               | `1.25rem`                  | Safe-area-aware shell padding                |
| `--idq-capture-shell-radius`                | `1.5rem`                   | Outer shell corners                          |
| `--idq-capture-shell-shadow`                | Soft neutral shadow        | Outer shell elevation                        |
| `--idq-capture-panel-radius`                | `1rem`                     | Large panels and media frames                |
| `--idq-capture-card-radius`                 | `0.75rem`                  | Cards and previews                           |
| `--idq-capture-control-radius`              | `0.625rem`                 | Buttons and file controls                    |
| `--idq-capture-focus-width`                 | `0.1875rem`                | Keyboard focus-ring width                    |
| `--idq-capture-focus-offset`                | `0.1875rem`                | Keyboard focus-ring offset                   |
| `--idq-capture-media-background`            | `#0c111d`                  | Camera and image-preview background          |
| `--idq-capture-overlay-background`          | Translucent near-black     | Camera prompt background                     |
| `--idq-capture-overlay-border`              | Translucent white          | Camera prompt border                         |
| `--idq-capture-overlay-foreground`          | `#ffffff`                  | Camera prompt text                           |
| `--idq-capture-face-guide`                  | Translucent white          | Primary live face guide                      |
| `--idq-capture-face-guide-muted`            | Translucent white          | Secondary live face guide                    |
| `--idq-capture-liveness-color`              | Strong accent              | Preparation illustration colour              |
| `--idq-capture-liveness-size`               | Responsive `9rem`–`12rem`  | Preparation illustration size                |
| `--idq-capture-liveness-duration`           | `9.6s`                     | Explanatory movement-loop duration           |
| `--idq-capture-liveness-cue-opacity`        | `0.14`                     | Resting directional-cue opacity              |
| `--idq-capture-liveness-cue-active-opacity` | `0.9`                      | Active directional-cue opacity               |
| `--idq-capture-motion-fast`                 | `160ms`                    | Colour feedback duration                     |
| `--idq-capture-motion-press`                | `140ms`                    | Press feedback duration                      |
| `--idq-capture-motion-ease-out`             | Strong ease-out curve      | Press feedback easing                        |
| `--idq-capture-motion-ease-in-out`          | Strong ease-in-out curve   | Explanatory movement easing                  |
| `--idq-capture-tap-highlight`               | Translucent blue           | Touch tap highlight                          |

System dark mode supplies accessible dark defaults for the semantic colour
variables, while host declarations still take precedence. Forced-colour and
reduced-motion safeguards remain package-owned. These variables customize the
local presentation only: they do not replace the signed portable
experience manifest or an immutable notice, and cannot change the
capture plan or assert assurance.

## Active liveness

`createActiveLivenessMethodAdapter` consumes the public acquisition-plan v1
contract and runs its challenges in the declared order. Every challenge has its
own deadline. The adapter keeps raw frames in memory, applies the plan's local
quality gates, releases the camera on success, failure, timeout, cancellation,
restart, or detachment, and passes the complete ordered frame set only to the
host-owned `submit` callback.

After the required notice response, Capture Web recommends the first
policy-ordered method on a single preparation page. For active liveness, that
page explains the short automatic capture, then one **Start Liveness Check**
action opens the camera and runs every ordered prompt. There is no intermediate
continue screen or second start action. When the profile allows alternatives,
**Use Another Method** keeps them explicitly available without making the
subject choose before seeing the recommended path. The preparation illustration
continuously demonstrates common left, right, up, and down head movements; the
actual capture prompts still come from the server-issued plan. Movement stops
under the subject's reduced-motion preference.

```ts
import { createActiveLivenessMethodAdapter, defineIdenqaCapture } from "@idenqa/capture";

defineIdenqaCapture();

const liveness = createActiveLivenessMethodAdapter({
  plan: serverIssuedAcquisitionPlan,
  requirementKey: "selfie",
  async assess(frame, requirement, challenge, signal) {
    return localQualityAssessor.measure(frame, requirement.quality, challenge, signal);
  },
  async submit(submission, signal) {
    // Submit through the tenant's Core/provider integration. Resolve only after
    // Core capture progress can return the exact accepted step.
    await submitActiveLiveness(submission, signal);
  },
});

await capture.start({
  baseUrl,
  captureToken,
  outcomeToken,
  capabilities,
  methodAdapters: [liveness],
});
```

The built-in assessment can enforce captured dimensions and byte size. If the
plan requires brightness, contrast, sharpness, glare, or face count, the host
must provide an assessor; an unavailable, non-finite, out-of-range, or
frame-contradicting measurement fails closed. Local prompt completion and
quality checks do not establish active-liveness assurance. A compatible
provider or model must evaluate the temporal evidence and return an
authoritative normalised result.

The self-hosted hosted demo exposes this path at
`/hosted.html?method=active-liveness`. It executes three camera prompts and uploads
every frame with ordered challenge/time metadata and a content-digest chain
through the Core evidence boundary. It completes only after authoritative Core
progress reports the exact step. That synthetic path proves orchestration,
cleanup, sequence upload, and receipt; it is not evidence of production liveness
or PAD performance.

## Extension acquisition methods

Any profile-approved owner-namespaced method, such as
`com.example.method.secure_nfc`, can register a programmatic adapter. The method
must also exist in the session's immutable evidence registry and be advertised
as implemented and currently available by the integration.

```ts
const secureNFC = {
  method: "com.example.method.secure_nfc",
  copy: {
    label: "Secure NFC",
    action: "Read Passport Chip",
    description: "Read the secure chip in your passport",
    preparation: "Hold your passport near this device.",
    instruction: "Keep the passport still until the secure read completes.",
  },
  async acquire(context, controls) {
    controls.update({ phase: "requesting_permission" });
    await readAndSubmitPassportChip(context, controls);
    // Returning does not complete the step. Capture Web refreshes Core.
  },
};

await capture.start({
  baseUrl,
  captureToken,
  outcomeToken,
  capabilities: {
    supportedMethods: [secureNFC.method],
    availableMethods: [secureNFC.method],
  },
  methodAdapters: [secureNFC],
});
```

Adapters receive only the exact verification, requirement, evidence type,
artefact, method, fallback, locale, and cancellation signal. Progress uses a
closed identifier-free vocabulary and preview accepts only a local
`MediaStream`. Raw evidence, NFC payloads, transcripts, provider results,
credentials, and asserted assurances must not enter progress, DOM attributes,
events, logs, or analytics. Missing, duplicate, ambiguous, or invalid adapters
fail startup. Returning before Core reports the exact completion produces a
retryable error rather than capture success.

The component tracks accepted evidence against the exact planned step. A native
progress indicator reports completed and total steps, including document
front/back and separate `all_of` acquisition legs. Once one method satisfies an
`any_of` step, its alternatives are removed for that capture run. Capture
completion is announced separately from verification completion and emits a
one-time `idenqa-capture-complete` event containing only the verification ID and
step counts. `idenqa-capture-progress` is emitted after each newly accepted
step; neither event contains evidence bytes, filenames, digests, or subject
data.

After the final accepted step, the guided journey moves directly to a processing
screen; it does not ask the subject to click a local “finish” button. Capture Web
reads `GET /v1/capture/outcome` and renders only Core's subject-safe authoritative
state. `verified`, `not_verified`, and `inconclusive` come from the immutable
policy decision attached to a completed session. Processing, action-required,
cancelled, expired, and operational-failure screens remain workflow states and
are not relabelled as identity decisions. The projection contains no policy
reasons, assurance, decision identifiers, provider/model details, evidence
metadata, or subject data. A valid, unrevoked outcome credential may read this
projection after the session stops accepting capture, including after session
expiry; it grants no capture authority. The capture credential cannot outlive
the session and is never used for this read. Capture Web neither accepts an
expired capture bearer nor infers authoritative expiry from browser time.

Each start also reads the authoritative accepted-step snapshot from Core. The
component reconstructs the immutable plan, including a persisted
`capture_failed` fallback selection, validates every completion against exactly
one current step, and restores progress without retaining evidence or upload
state in browser storage. A fully recovered plan emits the safe progress and
capture-complete events again for the new host page, but does not repeat the
evidence-accepted event or issue another upload.

During an active flow the component also opens the public SDK's versioned
WebSocket observation channel. It reports safe capture-step activity, emits
`idenqa-realtime-event` for host observation, including capture-safe
verification-check state/version progress, obtains a fresh display-once
ticket after retryable disconnects, and reuses stable command IDs when an
unsettled command must be replayed. Capture progress, verification-check
progress, state-change, and resync messages
always trigger a fresh REST session/authority/progress read; the socket never
becomes authoritative. Evidence bytes, filenames, digests, capture tokens, and
ticket URLs are never placed in realtime events.

If an authority permits multiple regions, pass the selected canonical `region`
to `start`; a single permitted region is selected automatically.

The server notice locale becomes the component's authoritative UI locale after
bootstrap. Package-owned interface copy resolves an optional host catalogue in
exact-locale, base-language, then built-in English order. Locale tags, catalogue
keys, and non-empty values are validated before use; numbers use `Intl`, and the
component sets `lang` and `dir`. The immutable legal notice title, summary,
purpose, consequences, controller, and recipient always remain exact server
copy and are never read from or replaced by the interface catalogue.

## Real self-hosted demonstration

The hosted and embedded demonstrations create a fresh synthetic policy, capture
profile, notice, verification session, processing authority, and display-once
capture and outcome credentials against a running self-hosted Core. The tenant
API key remains inside the Vite development server. The browser receives both
credentials only in a same-origin, no-store bootstrap response; neither is
placed in a URL, HTML, storage, event, or log.

Provide a local tenant API key with `policies:write`, `capture_profiles:write`,
`notices:write`, `verification_sessions:create`, and `authorities:write`. The demo
region must exactly match Core's `IDENQA_REGION` and must also be a canonical
processing-authority code, such as `tenant-local`.

```sh
IDENQA_DEMO_CORE_URL=http://127.0.0.1:8080 \
IDENQA_DEMO_TENANT_API_KEY='replace-with-local-tenant-api-key' \
IDENQA_DEMO_REGION=tenant-local \
pnpm --dir capture/web demo:hosted
```

Open `/hosted.html` for the standalone product surface or `/embedded.html` for
the same component inside a tenant-owned page. Use synthetic JPEG or PNG evidence
only. The local conformance harness may add `?outcome=not_verified`,
`inconclusive`, `action_required`, `cancelled`, `failed`, or `expired`; each option provisions
or invokes the corresponding real Core transition and never instructs the
component to manufacture an outcome. This harness is local demonstration infrastructure, not a production
tenant backend; production integrations must authenticate their own subject and
apply CSRF, origin, rate-limit, and session-linkage controls at their trusted
bootstrap boundary.

Vite serves the plain-HTML development fixture, Playwright exercises the
component in a real browser, and tsdown remains the published-library builder.
The second development fixture mounts the same custom element from React and
observes its composed completion event; React is not a production dependency or
part of the published bundle. Effect is not a dependency.

## Review-linked recapture handoff

`createRecaptureHandoff` connects an existing capture element to a tenant backend that authenticates
the subject and obtains a review-linked credential. The callback returns only
`{ verificationId, captureToken, outcomeToken, expiresAt, outcomeExpiresAt }`;
it must never return a tenant API key.

```ts
const handoff = createRecaptureHandoff(
  element,
  { baseUrl: coreUrl, capabilities },
  async (request) => {
    const response = await fetch("/my-verification/recapture", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ recoverVerificationId: request.recoverVerificationId }),
      signal: request.signal,
      cache: "no-store",
    });
    if (!response.ok) throw new Error("Recapture is unavailable.");
    return response.json();
  },
);
await handoff.start();
// After a backend-authorized credential replacement:
await handoff.recover();
```

The backend must authenticate the subject, protect against CSRF, and constrain the linked case
it may access. Recovery continues the same child, stops prior capture activity and checks the
Core session ID before rendering. Core restores accepted evidence with its original provenance
and requires fresh subject authorization. Credentials stay in memory; no URL or storage handoff
is used. Call `handoff.cancel()` when leaving the journey. This helper does not establish live
journey or visual/interaction acceptance for the development demo.
