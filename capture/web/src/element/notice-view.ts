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
        <button class="primary" type="button" @click=${() => view.beginJourney(flow)}>
          ${view.text("getStarted")}
        </button>
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
