import { css } from "lit";

export const baseStyles = css`
  :host {
    --idq-capture-accent: #2e6b4a;
    --idq-capture-accent-strong: #1f4e37;
    --idq-capture-accent-foreground: #ffffff;
    --idq-capture-background: #f6f4ee;
    --idq-capture-border: #e4e0d5;
    --idq-capture-card-radius: 0.875rem;
    --idq-capture-control-radius: 999px;
    --idq-capture-error: #b42318;
    --idq-capture-face-guide: rgb(255 255 255 / 78%);
    --idq-capture-face-guide-muted: rgb(255 255 255 / 42%);
    --idq-capture-face-guide-ready: #79e8b1;
    --idq-capture-font-family:
      Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    --idq-capture-focus-offset: 0.1875rem;
    --idq-capture-focus-width: 0.1875rem;
    --idq-capture-liveness-color: var(--idq-capture-accent-strong);
    --idq-capture-liveness-cue-active-opacity: 0.9;
    --idq-capture-liveness-cue-opacity: 0.14;
    --idq-capture-liveness-duration: 9.6s;
    --idq-capture-liveness-size: clamp(9rem, 34vw, 12rem);
    --idq-capture-media-background: #0c111d;
    --idq-capture-motion-ease-in-out: cubic-bezier(0.77, 0, 0.175, 1);
    --idq-capture-motion-ease-out: cubic-bezier(0.23, 1, 0.32, 1);
    --idq-capture-motion-fast: 160ms;
    --idq-capture-motion-press: 140ms;
    --idq-capture-muted: #5b6a61;
    --idq-capture-overlay-background: rgb(10 18 16 / 82%);
    --idq-capture-overlay-border: rgb(255 255 255 / 18%);
    --idq-capture-overlay-foreground: #ffffff;
    --idq-capture-panel-radius: 0.875rem;
    --idq-capture-shell-max-height: min(52.75rem, calc(100dvh - 3rem));
    --idq-capture-shell-max-width: 42rem;
    --idq-capture-shell-min-height: min(52.75rem, calc(100dvh - 3rem));
    --idq-capture-shell-padding: 1.5rem;
    --idq-capture-shell-radius: 0;
    --idq-capture-shell-shadow: none;
    --idq-capture-surface: #ffffff;
    --idq-capture-surface-strong: #e7f0e8;
    --idq-capture-tap-highlight: rgb(23 92 211 / 18%);
    --idq-capture-text: #16211b;
    color-scheme: light dark;
    color: var(--idq-capture-text);
    display: block;
    font-family: var(--idq-capture-font-family);
    line-height: 1.5;
  }

  :host([hidden]) {
    display: none;
  }

  * {
    box-sizing: border-box;
  }

  .shell {
    background: var(--idq-capture-background);
    border: 1px solid var(--idq-capture-border);
    border-radius: var(--idq-capture-shell-radius);
    box-shadow: var(--idq-capture-shell-shadow);
    margin-inline: auto;
    /*max-height: var(--idq-capture-shell-max-height);*/
    max-width: var(--idq-capture-shell-max-width);
    min-height: var(--idq-capture-shell-min-height);
    overflow-x: hidden;
    overflow-y: auto;
    overflow-wrap: anywhere;
    overscroll-behavior: contain;
    padding: max(var(--idq-capture-shell-padding), env(safe-area-inset-top))
      max(var(--idq-capture-shell-padding), env(safe-area-inset-right))
      max(var(--idq-capture-shell-padding), env(safe-area-inset-bottom))
      max(var(--idq-capture-shell-padding), env(safe-area-inset-left));
  }

  @media (min-width: 769px) and (min-height: 568px) {
    .shell {
      min-height: 600px;
      max-height: 100%;
      max-width: 400px;
    }
  }

  .visually-hidden {
    block-size: 1px;
    clip-path: inset(50%);
    inline-size: 1px;
    overflow: hidden;
    position: absolute;
    white-space: nowrap;
  }

  .eyebrow {
    color: var(--idq-capture-accent-strong);
    font-size: 0.75rem;
    font-weight: 700;
    letter-spacing: 0.08em;
    margin: 0 0 0.5rem;
    text-transform: uppercase;
  }

  h2,
  h3,
  h4,
  p,
  dl {
    margin-block-start: 0;
    margin-block-end: 0;
  }

  h2 {
    font-size: 1.75rem;
    font-weight: 800;
    letter-spacing: -0.03em;
    line-height: 1.06;
    text-wrap: balance;
  }

  h3 {
    font-size: 1.125rem;
    line-height: 1.35;
    text-wrap: balance;
  }

  h4 {
    font-size: 1rem;
    text-wrap: balance;
  }

  .intro,
  .requirement-copy,
  .notice-meta,
  .status {
    color: var(--idq-capture-muted);
  }

  .notice {
    background: var(--idq-capture-surface);
    border: 1px solid var(--idq-capture-border);
    border-radius: var(--idq-capture-card-radius);
    margin-block-start: 1.5rem;
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .notice h4 {
    font-size: 0.875rem;
    /*margin-block: 1rem 0.25rem;*/
  }

  .notice-copy {
    font-size: 0.875rem;
    white-space: pre-wrap;
  }

  .notice-meta {
    display: grid;
    font-size: 0.875rem;
    gap: 0.75rem;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 12rem), 1fr));
  }

  .notice-meta div {
    min-width: 0;
  }

  .notice-meta dt {
    font-weight: 700;
  }

  .notice-meta dd {
    margin-inline-start: 0;
  }

  .notice-actions {
    display: grid;
    gap: 0.625rem;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 11rem), 1fr));
    margin-block-start: 1rem;
  }

  .notice-guidance {
    border-inline-start: 0.25rem solid var(--idq-capture-accent);
    font-size: 0.875rem;
    padding-inline-start: 0.75rem;
  }

  .error {
    color: var(--idq-capture-error);
    font-weight: 650;
    font-size: 0.75rem;
  }

  .requirements {
    display: grid;
    gap: 1.25rem;
    margin-block-start: 1.5rem;
  }

  .progress-summary {
    background: var(--idq-capture-surface);
    border: 1px solid var(--idq-capture-border);
    border-radius: var(--idq-capture-card-radius);
    margin-block-start: 1.25rem;
    padding: 1rem;
  }

  .progress-copy,
  .completion-copy {
    margin-block-end: 0.5rem;
  }

  progress {
    accent-color: var(--idq-capture-accent);
    display: block;
    inline-size: 100%;
  }

  .completion-copy {
    color: var(--idq-capture-accent-strong);
    font-weight: 700;
    margin-block: 0.75rem 0;
  }

  .requirement {
    border-block-start: 1px solid var(--idq-capture-border);
    padding-block-start: 1.25rem;
  }

  .steps {
    display: grid;
    gap: 0.875rem;
    margin-block-start: 1rem;
  }

  .step {
    background: var(--idq-capture-surface);
    border: 1px solid var(--idq-capture-border);
    border-radius: var(--idq-capture-card-radius);
    padding: 1rem;
  }

  .step-complete {
    border-color: var(--idq-capture-accent);
  }

  .fallback {
    border-inline-start: 0.25rem solid var(--idq-capture-accent);
    color: var(--idq-capture-muted);
    font-size: 0.875rem;
    margin-block-end: 1rem;
    padding-inline-start: 0.75rem;
  }

  .methods {
    display: grid;
    gap: 0.625rem;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 11rem), 1fr));
  }
`;
