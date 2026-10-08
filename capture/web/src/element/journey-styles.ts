import { css } from "lit";

export const journeyStyles = css`
  button {
    align-items: center;
    appearance: none;
    background: var(--idq-capture-background);
    border: 1px solid var(--idq-capture-border);
    border-radius: var(--idq-capture-control-radius);
    color: var(--idq-capture-text);
    cursor: pointer;
    display: inline-flex;
    font: inherit;
    font-weight: 650;
    justify-content: center;
    min-height: 3.25rem;
    padding: 0.75rem 1rem;
    touch-action: manipulation;
    transition:
      background-color var(--idq-capture-motion-fast) ease,
      border-color var(--idq-capture-motion-fast) ease,
      color var(--idq-capture-motion-fast) ease,
      transform var(--idq-capture-motion-press) var(--idq-capture-motion-ease-out);
    -webkit-tap-highlight-color: var(--idq-capture-tap-highlight);
  }

  button:disabled {
    cursor: wait;
    opacity: 0.65;
  }

  button:active,
  button[aria-pressed="true"] {
    background: var(--idq-capture-accent);
    border-color: var(--idq-capture-accent);
    color: var(--idq-capture-accent-foreground);
  }

  button:active {
    transform: scale(0.98);
  }

  button:focus-visible {
    outline: var(--idq-capture-focus-width) solid var(--idq-capture-accent);
    outline-offset: var(--idq-capture-focus-offset);
  }

  .status {
    font-size: 0.875rem;
    margin-block: 0.75rem 0;
    min-height: 1.3125rem;
  }

  .screen {
    display: flex;
    flex-direction: column;
    gap: 1.5rem;
    margin-inline: auto;
    /*max-width: 34rem;
      min-height: calc(var(--idq-capture-shell-min-height) - 2 * var(--idq-capture-shell-padding));*/
  }

  .screen-copy {
    color: var(--idq-capture-muted);
    font-size: 0.875rem;
    margin-block-end: 0;
    max-width: 34rem;
  }

  .hero-icon,
  .state-icon {
    align-items: center;
    background: var(--idq-capture-surface-strong);
    border-radius: 50%;
    color: var(--idq-capture-accent-strong);
    display: inline-flex;
    font-size: 1rem;
    font-weight: 800;
    block-size: 4.125rem;
    inline-size: 4.125rem;
    justify-content: center;
  }

  .liveness-illustration {
    align-items: center;
    align-self: center;
    background: var(--idq-capture-surface-strong);
    border: 1px solid var(--idq-capture-border);
    border-radius: 50%;
    color: var(--idq-capture-liveness-color);
    display: flex;
    justify-content: center;
    justify-self: center;
    overflow: hidden;
    /*width: var(--idq-capture-liveness-size);*/
    /*height: var(--idq-capture-liveness-size);*/
    width: 100px;
    height: 100px;
  }

  .liveness-illustration svg {
    height: 78%;
    overflow: visible;
    width: 78%;
  }

  .liveness-illustration path,
  .liveness-illustration circle {
    fill: none;
    stroke: currentColor;
    stroke-linecap: round;
    stroke-linejoin: round;
    stroke-width: 4;
  }

  .liveness-head,
  .liveness-features,
  .liveness-direction {
    transform-box: fill-box;
    transform-origin: center;
  }

  .liveness-head {
    animation: idq-liveness-head-demo var(--idq-capture-liveness-duration)
      var(--idq-capture-motion-ease-in-out) infinite;
  }

  .liveness-features {
    animation: idq-liveness-gaze-demo var(--idq-capture-liveness-duration)
      var(--idq-capture-motion-ease-in-out) infinite;
  }

  .liveness-direction {
    animation-duration: var(--idq-capture-liveness-duration);
    animation-iteration-count: infinite;
    animation-timing-function: linear;
    opacity: var(--idq-capture-liveness-cue-opacity);
    stroke-width: 3;
  }

  .liveness-direction-left {
    --idq-liveness-cue-x: -0.25rem;
    --idq-liveness-cue-y: 0;
    animation-name: idq-liveness-direction-left-demo;
  }

  .liveness-direction-right {
    --idq-liveness-cue-x: 0.25rem;
    --idq-liveness-cue-y: 0;
    animation-name: idq-liveness-direction-right-demo;
  }

  .liveness-direction-up {
    --idq-liveness-cue-x: 0;
    --idq-liveness-cue-y: -0.25rem;
    animation-name: idq-liveness-direction-up-demo;
  }

  .liveness-direction-down {
    --idq-liveness-cue-x: 0;
    --idq-liveness-cue-y: 0.25rem;
    animation-name: idq-liveness-direction-down-demo;
  }

  .state-icon {
    block-size: 2.375rem;
    inline-size: 2.375rem;
  }

  .benefits,
  .tips {
    display: grid;
    gap: 0;
    list-style: none;
    margin: 0;
    padding: 0;
  }

  .benefits li,
  .tips li {
    align-items: flex-start;
    display: grid;
    gap: 0.6875rem;
    font-size: 0.875rem;
    grid-template-columns: 0.75rem 1fr;
  }

  .benefits {
    background: var(--idq-capture-surface);
    border: 1px solid var(--idq-capture-border);
    border-radius: 1rem;
    padding: 0.25rem 1rem;
  }

  .benefits li {
    border-block-end: 1px solid var(--idq-capture-border);
    padding-block: 0.75rem;
  }

  .benefits li:last-child {
    border-block-end: 0;
  }

  .benefits li::before,
  .tips li::before {
    align-items: center;
    background: transparent;
    color: var(--idq-capture-accent-strong);
    content: "✓";
    display: inline-flex;
    font-size: 0.75rem;
    font-weight: 900;
    block-size: 1.25rem;
    inline-size: 0.75rem;
    justify-content: center;
    margin-block-start: 0.1rem;
  }

  .tips li::before {
    background: var(--idq-capture-surface-strong);
    border-radius: 50%;
    block-size: 1.5rem;
    inline-size: 1.5rem;
  }

  .primary {
    background: var(--idq-capture-accent);
    border-color: var(--idq-capture-accent);
    color: var(--idq-capture-accent-foreground);
  }

  .primary:hover {
    background: var(--idq-capture-accent-strong);
    border-color: var(--idq-capture-accent-strong);
  }

  .quiet {
    background: transparent;
    border-color: transparent;
    color: var(--idq-capture-muted);
  }

  .journey-actions {
    display: grid;
    gap: 0.75rem;
    margin-block-start: auto;
  }

  .journey-actions.split {
    grid-template-columns: minmax(0, 1fr) minmax(0, 2fr);
  }

  .journey-actions.notice-actions {
    grid-template-columns: 1fr;
  }

  .journey-progress {
    align-items: center;
    display: grid;
    gap: 0.75rem;
    grid-template-columns: 1fr auto;
    margin-block-end: 2rem;
  }

  .journey-progress p {
    color: var(--idq-capture-muted);
    font-size: 0.8125rem;
    font-weight: 700;
    margin: 0;
  }

  .journey-progress progress {
    appearance: none;
    -webkit-appearance: none;
    background: var(--idq-capture-border);
    border: 0;
    border-radius: 999px;
    color: var(--idq-capture-accent);
    grid-column: 1 / -1;
    block-size: 0.4375rem;
    overflow: hidden;
  }

  .journey-progress progress::-webkit-progress-bar {
    background: var(--idq-capture-border);
    border-radius: 999px;
  }

  .journey-progress progress::-webkit-progress-value {
    background: var(--idq-capture-accent);
    border-radius: 999px;
  }

  .journey-progress progress::-moz-progress-bar {
    background: var(--idq-capture-accent);
    border-radius: 999px;
  }

  .method-list {
    display: grid;
    gap: 0.625rem;
  }

  .method-card {
    align-items: center;
    display: grid;
    gap: 0.75rem;
    grid-template-columns: auto 1fr auto;
    justify-content: initial;
    border-radius: 1rem;
    min-height: 4.75rem;
    padding: 1rem;
    text-align: start;
    font-size: 1rem;
    background: var(--idq-capture-surface);
  }

  .method-card .method-icon {
    align-items: center;
    background: var(--idq-capture-surface-strong);
    border-radius: 100%;
    color: var(--idq-capture-accent-strong);
    display: inline-flex;
    block-size: 2.75rem;
    inline-size: 2.75rem;
    justify-content: center;
  }

  .method-card .method-copy {
    display: grid;
    gap: 0.125rem;
  }

  .method-card small {
    color: var(--idq-capture-muted);
    font-weight: 500;
  }

  .method-card .chevron {
    color: var(--idq-capture-muted);
    font-size: 1.25rem;
  }

  .country-picker {
    align-items: center;
    display: grid;
    position: relative;
  }

  .country-search-icon {
    color: var(--idq-capture-muted);
    display: inline-flex;
    inset-inline-start: 1rem;
    pointer-events: none;
    position: absolute;
    z-index: 1;
  }

  .country-picker input {
    appearance: none;
    background: var(--idq-capture-surface);
    border: 1px solid var(--idq-capture-border);
    border-radius: 999px;
    color: var(--idq-capture-text);
    font: inherit;
    inline-size: 100%;
    min-height: 3.5rem;
    padding: 0.75rem 1rem 0.75rem 3rem;
  }

  .country-picker input:focus-visible {
    outline: var(--idq-capture-focus-width) solid var(--idq-capture-accent);
    outline-offset: var(--idq-capture-focus-offset);
  }

  .country-list {
    display: none;
    gap: 0.625rem;
    max-height: 19rem;
    overflow-y: auto;
    padding: 0.125rem;
  }

  .country-picker:focus-within + .country-list,
  .country-list:focus-within,
  .country-list-open {
    display: grid;
  }

  .country-option {
    background: var(--idq-capture-surface);
    border-radius: 1rem;
    display: grid;
    gap: 0.75rem;
    grid-template-columns: 1fr auto auto;
    justify-content: initial;
    text-align: start;
  }

  .country-option:hover {
    border-color: var(--idq-capture-accent);
  }

  .country-code,
  .country-option .chevron {
    color: var(--idq-capture-muted);
    font-size: 0.8125rem;
  }

  .country-option .chevron {
    font-size: 1.15rem;
  }

  .empty-state {
    color: var(--idq-capture-muted);
    margin: 0;
    padding: 1rem;
    text-align: center;
  }

  .secured-by {
    align-items: center;
    align-self: end;
    color: var(--idq-capture-text);
    display: flex;
    font-size: 0.9375rem;
    font-weight: 600;
    gap: 0.5rem;
    justify-content: flex-end;
    margin: 0;
  }

  .mini-mark {
    align-items: center;
    background: var(--idq-capture-accent);
    border-radius: 0.625rem;
    color: var(--idq-capture-accent-foreground);
    display: inline-flex;
    font-size: 1.125rem;
    font-weight: 800;
    block-size: 2rem;
    inline-size: 2rem;
    justify-content: center;
  }

  .preflight-intro {
    gap: 2rem;
    padding-block-start: 2.0625rem;
  }

  .preflight-intro > div:first-of-type {
    display: grid;
    gap: 0.625rem;
  }

  .preflight-intro .eyebrow,
  .preflight-intro h2,
  .preflight-intro .screen-copy {
    margin-block-end: 0;
  }

  .country-screen > div:nth-of-type(2) {
    padding-block-start: 1rem;
  }

  .capture-panel {
    /*background: var(--idq-capture-surface);
      border: 1px solid var(--idq-capture-border);
      border-radius: var(--idq-capture-panel-radius);*/
    display: grid;
    gap: 1rem;
    margin-left: calc(-1 * var(--idq-capture-shell-padding));
    margin-right: calc(-1 * var(--idq-capture-shell-padding));
    /*padding: 1rem;*/
  }

  .capture-panel .file-label,
  .capture-panel > button,
  .capture-panel .camera-actions button {
    min-height: 3.25rem;
  }

  .review-image {
    aspect-ratio: 4 / 3;
    background: var(--idq-capture-media-background);
    border-radius: var(--idq-capture-card-radius);
    display: block;
    inline-size: 100%;
    object-fit: contain;
  }

  .notice-screen .notice {
    margin-block-start: 0;
  }

  .notice-screen .notice-actions {
    grid-template-columns: 1fr;
  }

  .confirmation-card {
    align-items: center;
    background: var(--idq-capture-surface);
    border: 1px solid var(--idq-capture-border);
    border-radius: var(--idq-capture-panel-radius);
    display: flex;
    gap: 0.875rem;
    padding: 1rem;
  }

  .confirmation-card p {
    margin: 0;
    font-size: 0.8125rem;
  }

  .confirmation-card strong {
    display: block;
    font-size: 0.875rem;
  }

  .privacy-note {
    color: var(--idq-capture-muted);
    font-size: 0.8125rem;
    margin: 0;
    text-align: center;
  }

  .processing-indicator {
    align-items: center;
    display: flex;
    gap: 0.4rem;
    min-height: 2rem;
  }

  .processing-indicator span {
    animation: idq-pulse 1.2s ease-in-out infinite;
    background: var(--idq-capture-accent);
    border-radius: 50%;
    block-size: 0.55rem;
    inline-size: 0.55rem;
  }

  .processing-indicator span:nth-child(2) {
    animation-delay: 150ms;
  }

  .processing-indicator span:nth-child(3) {
    animation-delay: 300ms;
  }
`;
