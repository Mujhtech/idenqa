import type { SubjectResponseAction } from "@idenqa/sdk";
import { html, nothing } from "lit";
import type { CaptureActiveFlowSnapshot } from "../flow.js";
import type { CaptureLocalizer, CaptureMessageKey, CaptureMessageValues } from "../localisation.js";
import type { CapturePlan } from "../planner.js";
import { formatNumber } from "./copy.js";
import type { ComponentFlowState, ElementTemplate, JourneyPhase } from "./state.js";

/** Read-only presentation state and explicit callbacks; the element owns lifecycle effects. */
export interface NoticeViewContext {
  readonly text: (key: CaptureMessageKey, values?: CaptureMessageValues) => string;
  readonly localizer: CaptureLocalizer;
  readonly beginJourney: (flow: CaptureActiveFlowSnapshot) => void;
  readonly journeyPhase: JourneyPhase;
  readonly resumeJourney: (plan: CapturePlan, showRecovery?: boolean) => void;
  readonly flowState: ComponentFlowState;
  readonly respond: (action: SubjectResponseAction) => Promise<void>;
  readonly responseError: boolean;
}

export function renderIntroduction(
  view: NoticeViewContext,
  flow: CaptureActiveFlowSnapshot,
): ElementTemplate {
  const totalSteps = flow.plan.requirements.length;
  return renderIntroductionScreen(view, totalSteps, () => view.beginJourney(flow));
}

export function renderIntroductionPreview(
  view: NoticeViewContext,
  totalSteps: number,
): ElementTemplate {
  return renderIntroductionScreen(view, totalSteps);
}

/** Render the real notice surface with bounded sample authority copy for authoring previews. */
export function renderNoticePreview(view: NoticeViewContext): ElementTemplate {
  return html`
    <div class="screen notice-screen">
      <div>
        <p class="eyebrow">${view.text("noticeStep")}</p>
        <h2>${view.text("noticeTitle")}</h2>
        <p class="screen-copy">${view.text("noticeBody")}</p>
      </div>
      <article class="notice" aria-labelledby="idq-preview-notice-title">
        <h4 id="idq-preview-notice-title">Identity verification notice</h4>
        <p class="notice-copy">We will collect the identity information shown in this journey.</p>
        <h4>${view.text("whyInformationNeeded")}</h4>
        <p class="notice-copy">To verify your identity for the service you requested.</p>
        <h4>${view.text("ifYouRefuse")}</h4>
        <p class="notice-copy">The identity check cannot be completed.</p>
        <dl class="notice-meta">
          <div>
            <dt>${view.text("controller")}</dt>
            <dd>Your organisation</dd>
          </div>
          <div>
            <dt>${view.text("recipient")}</dt>
            <dd>Identity verification service</dd>
          </div>
        </dl>
        <p class="notice-guidance">${view.text("reviewConsent")}</p>
      </article>
      <div class="journey-actions notice-actions">
        <button class="primary" type="button" aria-disabled="true" tabindex="-1">
          ${view.text("agreeAndContinue")}
        </button>
        <button class="quiet" type="button" aria-disabled="true" tabindex="-1">
          ${view.text("cancel")}
        </button>
      </div>
    </div>
  `;
}

function renderIntroductionScreen(
  view: NoticeViewContext,
  totalSteps: number,
  beginJourney?: () => void,
): ElementTemplate {
  return html`
    <div class="screen">
      <span class="hero-icon" aria-hidden="true">✓</span>
      <div>
        <p class="eyebrow">${view.text("secureCapture")}</p>
        <h2>${view.text("introTitle")}</h2>
        <p class="screen-copy">${view.text("introBody")}</p>
      </div>
      <ul class="benefits">
        <li>
          ${view.text("introStepCount", { count: formatNumber(totalSteps, view.localizer.locale) })}
        </li>
        <li>${view.text("introPrivate")}</li>
        <li>${view.text("introDevice")}</li>
      </ul>
      <div class="journey-actions">
        ${
          beginJourney === undefined
            ? html`<button class="primary" type="button" aria-disabled="true" tabindex="-1">
                ${view.text("getStarted")}
              </button>`
            : html`<button class="primary" type="button" @click=${beginJourney}>
                ${view.text("getStarted")}
              </button>`
        }
      </div>
      ${renderSecuredBy(view)}
    </div>
  `;
}

export function renderNotice(
  view: NoticeViewContext,
  flow: CaptureActiveFlowSnapshot,
): ElementTemplate {
  const { authority, notice, latestResponse } = flow.authoritySnapshot;
  return html`
    <div class="screen notice-screen">
      <div>
        <p class="eyebrow">${view.text("noticeStep")}</p>
        <h2>${view.text("noticeTitle")}</h2>
        <p class="screen-copy">${view.text("noticeBody")}</p>
      </div>
      <article class="notice" aria-labelledby="idq-notice-title" lang=${notice.locale}>
        <h4 id="idq-notice-title">${notice.copy.title}</h4>
        <p class="notice-copy">${notice.copy.summary}</p>
        <h4>${view.text("whyInformationNeeded")}</h4>
        <p class="notice-copy">${notice.copy.purpose}</p>
        <h4>${view.text("ifYouRefuse")}</h4>
        <p class="notice-copy">${notice.copy.consequences}</p>
        <dl class="notice-meta">
          <div>
            <dt>${view.text("controller")}</dt>
            <dd>${notice.controller}</dd>
          </div>
          <div>
            <dt>${view.text("recipient")}</dt>
            <dd>${notice.recipient}</dd>
          </div>
        </dl>
        ${
          flow.status === "notice_required"
            ? html`<p class="notice-guidance">
                ${
                  authority.consentRequired
                    ? view.text("reviewConsent")
                    : view.text("reviewAcknowledgement")
                }
              </p>`
            : html`<p class="notice-guidance" role="status">
                ${
                  latestResponse?.action === "consent"
                    ? view.text("consentRecorded")
                    : view.text("acknowledgementRecorded")
                }
              </p>`
        }
      </article>
      ${
        flow.status === "notice_required"
          ? renderNoticeActions(view, authority.consentRequired)
          : view.journeyPhase === "notice" && flow.status === "capture_ready"
            ? html`<div class="journey-actions">
                <button class="primary" type="button" @click=${() => view.resumeJourney(flow.plan)}>
                  ${view.text("continue")}
                </button>
              </div>`
            : nothing
      }
    </div>
  `;
}

export function renderNoticeActions(
  view: NoticeViewContext,
  consentRequired: boolean,
): ElementTemplate {
  const busy = view.flowState === "responding";
  const acceptedAction: SubjectResponseAction = consentRequired ? "consent" : "acknowledge";
  return html`
    <div
      class="journey-actions notice-actions"
      role="group"
      aria-label=${view.text("noticeActionsLabel")}
    >
      <button
        class="primary"
        type="button"
        ?disabled=${busy}
        @click=${() => void view.respond(acceptedAction)}
      >
        ${
          busy
            ? view.text("recordingResponse")
            : consentRequired
              ? view.text("agreeAndContinue")
              : view.text("acknowledgeAndContinue")
        }
      </button>
      <button
        class="quiet"
        type="button"
        ?disabled=${busy}
        @click=${() => void view.respond("refuse")}
      >
        ${view.text("cancel")}
      </button>
    </div>
    ${
      view.responseError
        ? html`<p class="error" role="alert">${view.text("responseFailed")}</p>`
        : nothing
    }
  `;
}

export function renderSecuredBy(view: NoticeViewContext): ElementTemplate {
  return html`<p class="secured-by">
    <span>${view.text("securedBy")}</span><span class="mini-mark" aria-hidden="true">I</span>
  </p>`;
}
