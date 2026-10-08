import { html } from "lit";

export function shieldIcon() {
  return html`<svg
    width="24"
    height="24"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.8"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    focusable="false"
  >
    <path d="M12 3 20 6v5c0 5.2-3.4 8.6-8 10-4.6-1.4-8-4.8-8-10V6l8-3Z" />
    <path d="m8.7 12 2.1 2.1 4.7-4.8" />
  </svg>`;
}

export function searchIcon() {
  return html`<svg
    width="18"
    height="18"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.8"
    stroke-linecap="round"
    aria-hidden="true"
    focusable="false"
  >
    <circle cx="11" cy="11" r="7" />
    <path d="m20 20-4-4" />
  </svg>`;
}

export function documentIcon() {
  return html`<svg
    width="28"
    height="28"
    viewBox="0 0 40 28"
    fill="none"
    stroke="currentColor"
    stroke-width="1.6"
    aria-hidden="true"
    focusable="false"
  >
    <rect x="1" y="1" width="38" height="26" rx="3" />
    <circle cx="12" cy="10" r="4" />
    <path d="M5 22v-2a7 7 0 0 1 14 0v2ZM24 8h10M24 13h10M24 18h6" />
  </svg>`;
}

export function renderLivenessIllustration() {
  return html`
    <div class="liveness-illustration" aria-hidden="true">
      <svg viewBox="0 0 160 160" focusable="false">
        <g class="liveness-head">
          <path d="M45 73c0-29 14-46 35-46s35 17 35 46v15c0 29-15 47-35 47S45 117 45 88Z" />
          <path d="M46 67c12 0 15-12 28-12 9 0 13 7 22 7 8 0 14-5 18-11" />
          <path d="M39 84c-7-1-9 5-7 12 1 6 6 9 12 8M121 84c7-1 9 5 7 12-1 6-6 9-12 8" />
          <g class="liveness-features">
            <path d="M61 91h.01M99 91h.01M80 94v9M70 111c6 4 14 4 20 0" />
          </g>
        </g>
        <path d="M52 127c-19 5-29 15-33 25M108 127c19 5 29 15 33 25" />
        <circle cx="80" cy="81" r="58" stroke-dasharray="5 9" opacity=".35" />
        <path class="liveness-direction liveness-direction-left" d="M30 73l-7 7 7 7M23 80h12" />
        <path class="liveness-direction liveness-direction-right" d="m130 73 7 7-7 7M137 80h-12" />
        <path class="liveness-direction liveness-direction-up" d="m73 30 7-7 7 7M80 23v12" />
        <path class="liveness-direction liveness-direction-down" d="m73 130 7 7 7-7M80 137v-12" />
      </svg>
    </div>
  `;
}
