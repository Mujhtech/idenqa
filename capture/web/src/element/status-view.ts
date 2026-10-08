import { html } from "lit";
import type { CaptureOutcomeFlowSnapshot } from "../flow.js";
import type { CaptureMessageKey, CaptureMessageValues } from "../localisation.js";
import type { ElementTemplate } from "./state.js";

/** Read-only presentation state and explicit callbacks; the element owns lifecycle effects. */
export interface StatusViewContext {
  readonly text: (key: CaptureMessageKey, values?: CaptureMessageValues) => string;
}

export function renderNoFlow(view: StatusViewContext): ElementTemplate {
  return html`
    <div class="screen" role="status" aria-live="polite">
      <span class="state-icon" aria-hidden="true">!</span>
      <div>
        <h2>${view.text("noFlowTitle")}</h2>
        <p class="screen-copy">${view.text("noFlowBody")}</p>
      </div>
    </div>
  `;
}

export function renderLoading(view: StatusViewContext): ElementTemplate {
  return html`
    <div class="screen" role="status" aria-live="polite" aria-busy="true">
      <span class="state-icon" aria-hidden="true">…</span>
      <div>
        <h2>${view.text("preparingSecureCapture")}</h2>
        <p class="screen-copy">${view.text("preparingCaptureBody")}</p>
      </div>
      <div class="processing-indicator" aria-hidden="true">
        <span></span><span></span><span></span>
      </div>
    </div>
  `;
}

export function renderError(view: StatusViewContext): ElementTemplate {
  return html`
    <div class="screen">
      <span class="state-icon" aria-hidden="true">!</span>
      <div>
        <h2>${view.text("capturePreparationFailedTitle")}</h2>
        <p class="screen-copy" role="alert">${view.text("capturePreparationFailed")}</p>
      </div>
    </div>
  `;
}

export function renderCancelled(view: StatusViewContext): ElementTemplate {
  return html`
    <div class="screen">
      <span class="state-icon" aria-hidden="true">×</span>
      <div>
        <h2>${view.text("captureCancelledTitle")}</h2>
        <p class="screen-copy" role="status" aria-live="polite">${view.text("captureCancelled")}</p>
      </div>
    </div>
  `;
}

export function renderProcessing(view: StatusViewContext): ElementTemplate {
  return html`
    <div class="screen" aria-busy="true">
      <span class="state-icon" aria-hidden="true">…</span>
      <div role="status" aria-live="polite" aria-atomic="true">
        <p class="eyebrow">${view.text("processing")}</p>
        <h2>${view.text("processingTitle")}</h2>
        <p class="screen-copy">${view.text("processingBody")}</p>
      </div>
      <div class="processing-indicator" aria-hidden="true">
        <span></span><span></span><span></span>
      </div>
    </div>
  `;
}

export function renderAuthoritativeOutcome(
  view: StatusViewContext,
  flow: CaptureOutcomeFlowSnapshot,
): ElementTemplate {
  switch (flow.status) {
    case "processing":
      return renderProcessing(view);
    case "verified":
      return renderOutcomeScreen(
        view,
        "✓",
        view.text("verificationSuccess"),
        view.text("verificationSuccessTitle"),
        view.text("verificationSuccessBody"),
      );
    case "action_required":
      return renderOutcomeScreen(
        view,
        "!",
        view.text("actionRequired"),
        view.text("actionRequiredTitle"),
        view.text("actionRequiredBody"),
      );
    case "not_verified":
      return renderOutcomeScreen(
        view,
        "×",
        view.text("verificationUnsuccessful"),
        view.text("verificationUnsuccessfulTitle"),
        view.text("verificationUnsuccessfulBody"),
      );
    case "inconclusive":
      return renderOutcomeScreen(
        view,
        "!",
        view.text("verificationUnsuccessful"),
        view.text("verificationInconclusiveTitle"),
        view.text("verificationInconclusiveBody"),
      );
    case "expired":
      return renderOutcomeScreen(
        view,
        "!",
        view.text("complete"),
        view.text("verificationExpiredTitle"),
        view.text("verificationExpiredBody"),
      );
    case "failed":
      return renderOutcomeScreen(
        view,
        "!",
        view.text("complete"),
        view.text("verificationFailedTitle"),
        view.text("verificationFailedBody"),
      );
    case "cancelled":
      return renderOutcomeScreen(
        view,
        "×",
        view.text("complete"),
        view.text("captureCancelledTitle"),
        view.text("verificationCancelledBody"),
      );
  }
}

export function renderOutcomeScreen(
  view: StatusViewContext,
  icon: string,
  eyebrow: string,
  title: string,
  body: string,
): ElementTemplate {
  return html`
    <div class="screen">
      <span class="hero-icon" aria-hidden="true">${icon}</span>
      <div role="status" aria-live="polite" aria-atomic="true">
        <p class="eyebrow">${eyebrow}</p>
        <h2>${title}</h2>
        <p class="screen-copy">${body}</p>
      </div>
      <p class="privacy-note">${view.text("closePage")}</p>
    </div>
  `;
}

export function renderComplete(view: StatusViewContext): ElementTemplate {
  return html`
    <div class="screen">
      <span class="hero-icon" aria-hidden="true">✓</span>
      <div role="status" aria-live="polite">
        <p class="eyebrow">${view.text("complete")}</p>
        <h2>${view.text("completeTitle")}</h2>
        <p class="screen-copy">${view.text("completeBody")}</p>
      </div>
      <p class="privacy-note">${view.text("closePage")}</p>
    </div>
  `;
}

export function renderRefused(view: StatusViewContext): ElementTemplate {
  return html`
    <div class="screen">
      <span class="state-icon" aria-hidden="true">×</span>
      <div>
        <h2>${view.text("refusalTitle")}</h2>
        <p class="screen-copy" role="status">${view.text("refusalRecorded")}</p>
      </div>
    </div>
  `;
}

export function renderAuthorityBlocked(view: StatusViewContext): ElementTemplate {
  return html`
    <div class="screen">
      <span class="state-icon" aria-hidden="true">!</span>
      <div>
        <h2>${view.text("authorityBlockedTitle")}</h2>
        <p class="screen-copy error" role="alert">${view.text("authorityBlocked")}</p>
      </div>
    </div>
  `;
}
