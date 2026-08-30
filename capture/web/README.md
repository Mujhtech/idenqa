# Idenqa Web capture

`@idenqa/capture` is the open-source, framework-neutral Web capture package for
Idenqa Core. It consumes only public `@idenqa/sdk` contracts and does not require
Console or managed Cloud.

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

The `captureToken` should come from the tenant's trusted bootstrap flow. Do not
place it in HTML, a query string, persistent browser storage, analytics, or logs.
Integrated file-upload steps use a labelled native picker, apply the profile's
JPEG/PNG and byte constraints, check the file signature, compute its SHA-256
digest with Web Crypto, and upload the raw `Blob` through a requirement-bound
intent. Filenames are not sent. Interrupted attempts recover the current upload
ETag before restarting from byte zero.

Integrated live-camera steps request only the camera facing the required
artefact, wait for a usable preview frame, let the subject review or retake the
photo, and upload it with the distinct `idenqa.method.live_camera` binding. The
camera and temporary object URL are released on acceptance, retake,
cancellation, restart, or component detachment. A camera failure activates a
file-upload alternative only when the immutable profile explicitly permits a
`capture_failed` fallback; cancelling the camera does not activate that
fallback.

The component tracks accepted evidence against the exact planned step. A native
progress indicator reports completed and total steps, including document
front/back and separate `all_of` acquisition legs. Once one method satisfies an
`any_of` step, its alternatives are removed for that capture run. Capture
completion is announced separately from verification completion and emits a
one-time `idenqa-capture-complete` event containing only the verification ID and
step counts. `idenqa-capture-progress` is emitted after each newly accepted
step; neither event contains evidence bytes, filenames, digests, or subject
data.

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

Vite serves the plain-HTML development fixture, Playwright exercises the
component in a real browser, and tsdown remains the published-library builder.
The second development fixture mounts the same custom element from React and
observes its composed completion event; React is not a production dependency or
part of the published bundle. Effect is not a dependency.
