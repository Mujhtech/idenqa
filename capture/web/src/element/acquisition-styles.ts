import { css } from "lit";

export const acquisitionStyles = css`
  .file-option {
    display: grid;
    gap: 0.5rem;
  }

  .file-input {
    block-size: 1px;
    clip-path: inset(50%);
    inline-size: 1px;
    overflow: hidden;
    position: absolute;
    white-space: nowrap;
  }

  .file-label {
    align-items: center;
    background: var(--idq-capture-background);
    border: 1px solid var(--idq-capture-border);
    border-radius: var(--idq-capture-control-radius);
    cursor: pointer;
    display: inline-flex;
    font-weight: 650;
    justify-content: center;
    min-height: 2.75rem;
    padding: 0.625rem 0.875rem;
    touch-action: manipulation;
    -webkit-tap-highlight-color: var(--idq-capture-tap-highlight);
  }

  .file-input:focus-visible + .file-label {
    outline: var(--idq-capture-focus-width) solid var(--idq-capture-accent);
    outline-offset: var(--idq-capture-focus-offset);
  }

  .file-label:hover {
    border-color: var(--idq-capture-accent);
  }

  .file-input:disabled + .file-label {
    cursor: wait;
    opacity: 0.65;
  }

  .file-guidance {
    color: var(--idq-capture-muted);
    font-size: 0.8125rem;
    margin: 0;
  }

  .camera-option {
    display: grid;
    gap: 0.75rem;
    grid-column: 1 / -1;
    min-width: 0;
  }

  .camera-preview {
    aspect-ratio: 4 / 3;
    background: var(--idq-capture-media-background);
    border-radius: var(--idq-capture-control-radius);
    display: block;
    height: auto;
    max-height: 32rem;
    object-fit: contain;
    width: 100%;
  }

  .camera-actions {
    display: grid;
    gap: 0.625rem;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 10rem), 1fr));
  }

  .camera-guidance {
    color: var(--idq-capture-muted);
    font-size: 0.875rem;
    margin: 0;
  }

  .document-preview-frame {
    position: relative;
  }

  .document-guide {
    block-size: 100%;
    inline-size: 100%;
    inset: 0;
    pointer-events: none;
    position: absolute;
  }

  .document-guide-target {
    fill: none;
    stroke: var(--idq-capture-face-guide);
    stroke-dasharray: 14 12;
    stroke-width: 3.5;
  }

  .document-guide-quad {
    fill: none;
    stroke: var(--idq-capture-face-guide);
    stroke-linejoin: round;
    stroke-width: 5;
  }

  .document-guide[data-state="steadying"] .document-guide-target {
    stroke-dasharray: 6 6;
    stroke-width: 4.5;
  }

  .document-guide[data-state="ready"] .document-guide-target {
    stroke-dasharray: none;
    stroke-width: 7;
  }

  .adapter-option {
    display: grid;
    gap: 1rem;
    min-width: 0;
  }

  .adapter-preview-frame {
    background: var(--idq-capture-media-background);
    /*border-radius: var(--idq-capture-panel-radius);*/
    overflow: hidden;
    position: relative;
  }

  .adapter-option[data-presentation="active_liveness"] .adapter-preview-frame {
    aspect-ratio: 4 / 3;
  }

  .adapter-option[data-presentation="active_liveness"] .camera-preview {
    border-radius: 0;
    height: 100%;
    max-height: none;
    object-fit: cover;
    transform: scaleX(-1);
  }

  .liveness-face-guide {
    inset: 6%;
    width: 88%;
    height: 88%;
    pointer-events: none;
    position: absolute;
  }

  .liveness-guide-arc {
    fill: none;
    stroke: var(--idq-capture-face-guide);
    stroke-width: 1.1;
    stroke-linecap: round;
    transition: stroke var(--idq-capture-motion-fast) ease;
  }

  .liveness-face-guide[data-stage="centered"] .liveness-guide-arc {
    stroke: var(--idq-capture-face-guide-ready);
  }

  .liveness-face-guide[data-stage="pose"] .liveness-guide-arc {
    stroke: var(--idq-capture-face-guide-muted);
  }

  .liveness-face-guide line {
    stroke: var(--idq-capture-face-guide);
    stroke-width: 0.55;
    stroke-linecap: round;
    transition: stroke var(--idq-capture-motion-fast) ease;
  }
  .liveness-face-guide line[data-filled="true"] {
    stroke: var(--idq-capture-face-guide-ready);
  }
  .liveness-pose-meter {
    width: 100%;
    accent-color: var(--idq-capture-accent);
  }

  .liveness-overlay-prompt {
    background: var(--idq-capture-overlay-background);
    border: 1px solid var(--idq-capture-overlay-border);
    border-radius: var(--idq-capture-control-radius);
    color: var(--idq-capture-overlay-foreground);
    font-size: 0.75rem;
    font-weight: 500;
    inset-block-start: 50%;
    inset-inline: 50% auto;
    max-width: calc(100% - 2rem);
    width: max-content;
    padding: 0.625rem 1rem;
    pointer-events: none;
    position: absolute;
    text-align: center;
    transform: translate(-50%, -50%);
  }

  [dir="rtl"] .liveness-overlay-prompt {
    transform: translate(50%, -50%);
  }

  .liveness-auto-capture,
  .document-auto-capture {
    align-items: center;
    background: var(--idq-capture-surface-strong);
    border-radius: 999px;
    color: var(--idq-capture-accent-strong);
    display: inline-flex;
    font-size: 0.8125rem;
    font-weight: 700;
    gap: 0.4rem;
    justify-self: start;
    margin: 0;
    padding: 0.4rem 0.75rem;
  }

  .liveness-auto-capture::before,
  .document-auto-capture::before {
    content: "●";
    font-size: 0.55rem;
  }

  .adapter-progress {
    /*background: var(--idq-capture-surface);
      border: 1px solid var(--idq-capture-border);
      border-radius: var(--idq-capture-card-radius);
      padding: 1rem;*/
    margin-right: var(--idq-capture-shell-padding);
    margin-left: var(--idq-capture-shell-padding);
    display: flex;
    flex-direction: column;
    gap: 0.625rem;
    align-items: center;
  }

  .adapter-progress p {
    margin: 0;
  }

  .adapter-challenge-label {
    font-size: 0.75rem;
  }

  .liveness-progress-panel {
    --idq-liveness-background: #0b1710;
    --idq-liveness-muted: #b3b8b0;
    --idq-liveness-segment: #374139;
    --idq-liveness-segment-active: #2d6d4e;
    --idq-liveness-badge-background: #182e20;
    --idq-liveness-badge-foreground: #f5f4ee;
    --idq-liveness-badge-dot: #8bcea5;
    /*background: var(--idq-liveness-background);*/
    gap: 1rem;
    margin-inline: 0;
    padding: 2.25rem 1.5rem 1.75rem;
    text-align: center;
  }

  .liveness-progress-label {
    color: var(--idq-liveness-muted);
    font-size: 0.9375rem;
    font-weight: 500;
    text-wrap: balance;
  }

  .liveness-segments {
    display: grid;
    gap: 0.4375rem;
    grid-auto-columns: minmax(0, 1fr);
    grid-auto-flow: column;
    inline-size: 10rem;
    max-inline-size: 100%;
  }

  .liveness-segment {
    background: var(--idq-liveness-segment);
    block-size: 0.3125rem;
    border-radius: 999px;
  }

  .liveness-segment[data-state="active"],
  .liveness-segment[data-state="complete"] {
    background: var(--idq-liveness-segment-active);
  }

  .liveness-progress-panel .liveness-auto-capture {
    /*background: var(--idq-liveness-badge-background);
      color: var(--idq-liveness-badge-foreground);*/
    background: #262626;
    color: #fff;
    font-size: 0.75rem;
    font-weight: 500;
    gap: 0.5rem;
    max-inline-size: 100%;
    padding: 0.5rem 1.25rem;
  }

  .liveness-progress-panel .liveness-auto-capture::before {
    background: var(--idq-liveness-badge-dot);
    block-size: 0.5rem;
    border-radius: 50%;
    content: "";
    flex: 0 0 0.5rem;
    inline-size: 0.5rem;
  }

  .adapter-prompt {
    font-size: clamp(1.125rem, 4vw, 1.4rem);
    font-weight: 750;
    text-wrap: balance;
  }
`;
