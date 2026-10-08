import { css } from "lit";

export const responsiveStyles = css`
  @media (prefers-color-scheme: dark) {
    :host {
      --idq-capture-accent: #54cbb2;
      --idq-capture-accent-strong: #8ee8d4;
      --idq-capture-accent-foreground: #071411;
      --idq-capture-background: #121b19;
      --idq-capture-border: #36433f;
      --idq-capture-muted: #aab9b4;
      --idq-capture-surface: #1b2824;
      --idq-capture-surface-strong: #233d36;
      --idq-capture-text: #f3f7f5;
      --idq-capture-error: #ffb4ab;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    button,
    .liveness-guide-arc,
    .liveness-face-guide line {
      transition-duration: 0ms;
    }

    .processing-indicator span {
      animation: none;
      opacity: 1;
      transform: none;
    }

    .liveness-head,
    .liveness-features,
    .liveness-direction {
      animation: none;
      transform: none;
    }

    .liveness-direction {
      opacity: var(--idq-capture-liveness-cue-opacity);
    }
  }

  @media (max-width: 30rem) {
    .shell {
      border-inline: 0;
      border-radius: 0;
      box-shadow: none;
      max-height: none;
      min-height: 100dvh;
      overflow-y: visible;
    }

    .screen {
      min-height: calc(100dvh - 2 * var(--idq-capture-shell-padding));
    }

    .journey-actions.split {
      grid-template-columns: 1fr;
    }

    .adapter-option[data-presentation="active_liveness"] .adapter-preview-frame {
      aspect-ratio: 3 / 4;
      margin-inline: -1rem;
    }
  }

  @media (hover: hover) and (pointer: fine) {
    button:hover {
      border-color: var(--idq-capture-accent);
    }
  }

  .shell.document-camera-shell {
    background: #080808;
    color: #fff;
    border: none;
    --idq-capture-text: #fff;
    --idq-capture-muted: #c9c9c9;
    --idq-capture-media-background: #080808;
  }

  .document-camera-shell .journey-progress {
    position: absolute;
    inline-size: 1px;
    block-size: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }

  .document-camera {
    background: #080808;
    color: #fff;
    padding: clamp(1rem, 4vw, 2rem);
    border-radius: 0.75rem;
    gap: 1.25rem;
  }

  .document-camera-shell .document-camera {
    padding: 0;
    border-radius: 0;
    padding-block-start: 0.5rem;
  }

  .document-navigation {
    display: flex;
    justify-content: space-between;
    gap: 1rem;
  }

  .document-camera-shell .document-navigation button {
    background: transparent;
    color: #c9c9c9;
    border-color: transparent;
    padding: 0.5rem;
    min-block-size: 2.75rem;
    font-size: 0.875rem;
  }

  .document-camera .document-instruction {
    background: #262626;
    color: #fff;
    border-radius: 0.5rem;
    padding: 0.875rem;
    font-size: 0.725rem;
    font-weight: 500;
    line-height: 1.6;
    text-align: center;
    margin: 0;
    letter-spacing: normal;
    text-wrap: pretty;
  }

  .document-viewfinder {
    border: 3px solid #fff;
    border-radius: 0.875rem;
    overflow: hidden;
    min-inline-size: 0;
  }

  .document-camera .camera-preview {
    background: #080808;
    border-radius: 0;
    max-height: none;
  }

  .document-camera-placeholder {
    aspect-ratio: 4 / 3;
    display: grid;
    place-items: center;
    color: #737373;
  }

  .document-camera-placeholder svg {
    inline-size: 4rem;
    block-size: auto;
  }

  .document-side-label {
    display: flex;
    align-items: center;
    gap: 0.875rem;
    padding: 1rem;
    background: #e8e8e8;
    color: #424242;
    font-size: 0.9375rem;
    line-height: 1.5;
  }

  .document-side-label svg {
    flex-shrink: 0;
    color: #19166b;
  }
  .document-camera .document-guide-target {
    stroke: #fff;
    stroke-dasharray: none;
  }
  .document-camera .document-guide-quad {
    stroke: #a7f3d0;
  }

  .document-feedback,
  .document-auto-copy {
    color: #c9c9c9;
    text-align: center;
    font-size: 0.725rem;
    margin: 0;
  }

  .document-feedback:empty {
    display: none;
  }
  .document-help {
    text-align: center;
  }
  .document-help summary {
    cursor: pointer;
    padding: 0.75rem;
    min-block-size: 2.75rem;
    box-sizing: border-box;
    list-style-position: inside;
    font-size: 0.725rem;
  }
  .document-help p {
    color: #c9c9c9;
    font-size: 0.75rem;
    line-height: 1.6;
  }
  .document-help summary:focus-visible {
    outline: 3px solid #fff;
    outline-offset: 3px;
    border-radius: 0.25rem;
  }

  .document-camera .document-controls {
    grid-template-columns: 1fr;
    margin-block-start: auto;
    padding-block-start: 1.5rem;
  }

  .document-camera[data-state="reviewing"] .document-controls {
    padding-block-start: clamp(2rem, 10vh, 5rem);
  }

  .document-camera button,
  .document-camera-shell .journey-actions button {
    background: transparent;
    color: #fff;
    border-color: #606060;
    min-block-size: 3rem;
  }

  .document-camera button.primary {
    background: #fff;
    border-color: #fff;
    color: #171717;
  }
  .document-camera button:hover,
  .document-camera-shell .journey-actions button:hover {
    background: #262626;
  }
  .document-camera button.primary:hover {
    background: #e8e8e8;
  }
  .document-camera button:focus-visible,
  .document-camera-shell .journey-actions button:focus-visible {
    outline-color: #fff;
  }
  .document-camera .error {
    background: #321b1b;
    color: #ffd6d6;
  }
  .document-shutter {
    display: flex;
    gap: 0.75rem;
    align-items: center;
    justify-content: center;
  }
  .shutter-icon {
    inline-size: 1rem;
    block-size: 1rem;
    border: 1px solid #fff;
    border-radius: 50%;
    box-shadow: inset 0 0 0 3px #080808;
    background: #fff;
  }

  @media (forced-colors: active) {
    .journey-progress progress {
      --idq-capture-accent: Highlight;
      --idq-capture-border: Canvas;
      border: 1px solid CanvasText;
      forced-color-adjust: none;
    }
    .liveness-progress-panel {
      --idq-liveness-background: Canvas;
      --idq-liveness-muted: CanvasText;
      --idq-liveness-segment: Canvas;
      --idq-liveness-segment-active: Highlight;
      --idq-liveness-badge-background: Canvas;
      --idq-liveness-badge-foreground: CanvasText;
      --idq-liveness-badge-dot: CanvasText;
    }
    .liveness-segment {
      border: 1px solid CanvasText;
      forced-color-adjust: none;
    }
    .liveness-progress-panel .liveness-auto-capture {
      border: 1px solid CanvasText;
    }
    .document-camera,
    .shell.document-camera-shell,
    .document-camera .document-instruction,
    .document-side-label {
      background: Canvas;
      color: CanvasText;
    }
    .document-viewfinder {
      border-color: CanvasText;
    }
    .document-camera .document-guide-target {
      stroke: CanvasText;
    }
    button[aria-pressed="true"] {
      outline: 0.1875rem solid ButtonText;
    }
  }
`;
