import { html, nothing } from "lit";
import type { CaptureLocalizer, CaptureMessageKey, CaptureMessageValues } from "../localisation.js";
import { formatNumber } from "./copy.js";
import { searchIcon, shieldIcon } from "./icons.js";
import type { CountryJourneyPhase, ElementTemplate } from "./state.js";
import type { CaptureCountryJourneyOptions, CaptureCountryOption } from "./types.js";

/** Read-only presentation state and explicit callbacks; the element owns lifecycle effects. */
export interface CountryViewContext {
  readonly countryJourney: CaptureCountryJourneyOptions | undefined;
  readonly countryJourneyPhase: CountryJourneyPhase;
  readonly text: (key: CaptureMessageKey, values?: CaptureMessageValues) => string;
  readonly localizer: CaptureLocalizer;
  readonly renderSecuredBy: () => ElementTemplate;
  readonly countryQuery: string;
  readonly countrySelecting: string | undefined;
  readonly selectCountry: (country: CaptureCountryOption) => Promise<void>;
  readonly countrySelectionError: boolean;
  readonly cancel: () => void;
  readonly countryShowNotice: () => void;
  readonly countrySearch: (query: string) => void;
  readonly countryBack: () => void;
  readonly countryContinue: () => void;
}

export function renderCountryJourney(view: CountryViewContext): ElementTemplate {
  const journey = view.countryJourney;
  if (journey === undefined) return nothing;
  if (view.countryJourneyPhase === "intro") {
    return html`
      <div class="screen preflight-intro">
        <span class="hero-icon" aria-hidden="true">${shieldIcon()}</span>
        <div>
          <p class="eyebrow">${view.text("secureCapture")}</p>
          <h2>${view.text("introTitle")}</h2>
          <p class="screen-copy">${view.text("introBody")}</p>
        </div>
        <ul class="benefits">
          <li>
            ${
              journey.captureItemCount === undefined
                ? view.text("countryMatchedDocuments")
                : view.text("introStepCount", {
                    count: formatNumber(journey.captureItemCount, view.localizer.locale),
                  })
            }
          </li>
          <li>${view.text("introPrivate")}</li>
          <li>${view.text("introDevice")}</li>
        </ul>
        <div class="journey-actions">
          <button class="primary" type="button" @click=${() => view.countryShowNotice()}>
            ${view.text("getStarted")}
          </button>
        </div>
        ${view.renderSecuredBy()}
      </div>
    `;
  }

  if (view.countryJourneyPhase === "notice") return renderCountryJourneyNotice(view, journey);

  const query = view.countryQuery.trim().toLocaleLowerCase(view.localizer.locale);
  const countries = journey.countries.filter(
    (country) =>
      query.length === 0 ||
      country.label.toLocaleLowerCase(view.localizer.locale).includes(query) ||
      country.code.toLocaleLowerCase(view.localizer.locale).includes(query),
  );
  return html`
    <div class="screen country-screen">
      ${
        journey.captureItemCount === undefined
          ? nothing
          : html`
              <div class="journey-progress">
                <p>
                  ${view.text("stepOf", {
                    current: formatNumber(1, view.localizer.locale),
                    total: formatNumber(journey.captureItemCount, view.localizer.locale),
                  })}
                </p>
                <p>
                  ${view.text("progressPercent", { percent: formatNumber(0, view.localizer.locale) })}
                </p>
                <progress aria-label=${view.text("progressLabel")} value="0" max="100"></progress>
              </div>
            `
      }
      <div>
        <h2>${view.text("chooseCountryTitle")}</h2>
        <p class="screen-copy">${view.text("chooseCountryBody")}</p>
      </div>
      <div class="country-picker">
        <label class="visually-hidden" for="country-search">${view.text("searchCountry")}</label>
        <span class="country-search-icon" aria-hidden="true">${searchIcon()}</span>
        <input
          id="country-search"
          type="search"
          autocomplete="country-name"
          placeholder=${view.text("searchCountry")}
          .value=${view.countryQuery}
          ?disabled=${view.countrySelecting !== undefined}
          @input=${(event: InputEvent) => view.countrySearch((event.currentTarget as HTMLInputElement).value)}
        />
      </div>
      <div
        class=${query.length === 0 ? "country-list" : "country-list country-list-open"}
        role="list"
        aria-live="polite"
      >
        ${countries.map(
          (country) => html`
            <button
              class="country-option"
              type="button"
              ?disabled=${view.countrySelecting !== undefined}
              @click=${() => void view.selectCountry(country)}
            >
              <span>${country.label}</span>
              <span class="country-code">${country.code}</span>
              <span class="chevron" aria-hidden="true">›</span>
            </button>
          `,
        )}
        ${
          countries.length === 0
            ? html`<p class="empty-state">${view.text("countryNoResults")}</p>`
            : nothing
        }
      </div>
      ${
        view.countrySelecting === undefined
          ? nothing
          : html`<p class="status" role="status">${view.text("preparingCountry")}</p>`
      }
      ${
        view.countrySelectionError
          ? html`<p class="error" role="alert">${view.text("countrySelectionFailed")}</p>`
          : nothing
      }
      <div class="journey-actions">
        <button
          class="quiet"
          type="button"
          ?disabled=${view.countrySelecting !== undefined}
          @click=${() => view.countryBack()}
        >
          ${view.text("back")}
        </button>
      </div>
    </div>
  `;
}

export function renderCountryJourneyNotice(
  view: CountryViewContext,
  journey: CaptureCountryJourneyOptions,
): ElementTemplate {
  const { notice } = journey;
  return html`
    <div class="screen notice-screen">
      <div>
        <p class="eyebrow">${view.text("noticeStep")}</p>
        <h2>${view.text("noticeTitle")}</h2>
        <p class="screen-copy">${view.text("noticeBody")}</p>
      </div>
      <article class="notice" aria-labelledby="idq-notice-title" lang=${notice.locale}>
        <div>
          <h4 id="idq-notice-title">${notice.copy.title}</h4>
          <p class="notice-copy">${notice.copy.summary}</p>
        </div>
        <div>
          <h4>${view.text("whyInformationNeeded")}</h4>
          <p class="notice-copy">${notice.copy.purpose}</p>
        </div>
        <div>
          <h4>${view.text("ifYouRefuse")}</h4>
          <p class="notice-copy">${notice.copy.consequences}</p>
        </div>
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
        <p class="notice-guidance">
          ${notice.consentRequired ? view.text("reviewConsent") : view.text("reviewAcknowledgement")}
        </p>
      </article>
      <div
        class="journey-actions notice-actions"
        role="group"
        aria-label=${view.text("noticeActionsLabel")}
      >
        <button class="primary" type="button" @click=${() => view.countryContinue()}>
          ${notice.consentRequired ? view.text("agreeAndContinue") : view.text("acknowledgeAndContinue")}
        </button>
        <button class="quiet" type="button" @click=${() => view.cancel()}>
          ${view.text("cancel")}
        </button>
      </div>
    </div>
  `;
}
