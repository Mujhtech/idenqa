import { LitElement, css, html, nothing, type PropertyValues } from "lit";

import type {
  CaptureRealtimeEvent,
  ExperienceResolution,
  SubjectResponseAction,
} from "@idenqa/sdk";
import type { EvidenceUpload } from "@idenqa/sdk";

import {
  createCaptureFlowController,
  isActiveCaptureFlowSnapshot,
  type CaptureActiveFlowSnapshot,
  type CaptureOutcomeFlowSnapshot,
  type CaptureFlowController,
  type CaptureFlowSnapshot,
  type CaptureFlowStartOptions,
} from "./flow.js";
import type { CapturePlan, CapturePlanStep } from "./planner.js";
import type { CaptureRuntimeFallbackReason } from "./planner.js";
import {
  CaptureCameraError,
  LIVE_CAMERA_METHOD,
  cameraFacingMode,
  captureCameraFrame,
  startCamera,
  stopCamera,
  type CameraFrame,
} from "./camera.js";
import { FILE_UPLOAD_METHOD, fileUploadPolicy, formatBytes } from "./upload.js";
import {
  DOCUMENT_GUIDE_ASPECT_RATIO,
  captureCorrectedDocumentFrame,
  createDocumentFrameObserver,
  isDocumentArtefact,
  normalizeCaptureDocumentCaptureOptions,
  type CaptureDocumentCaptureOptions,
  type DocumentFrameObserver,
  type NormalizedCaptureDocumentCaptureOptions,
} from "./document-capture.js";
import {
  createDocumentCaptureGate,
  type DocumentCaptureGate,
  type DocumentCaptureGateResult,
  type DocumentDetection,
  type DocumentQuad,
} from "./document-detection.js";
import {
  createCaptureLocalizer,
  type CaptureLocalizer,
  type CaptureMessageCatalogue,
  type CaptureMessageKey,
  type CaptureMessageValues,
} from "./localisation.js";
import {
  CaptureMethodAdapterError,
  captureMethodAdapterCopy,
  findCaptureMethodAdapter,
  normalizeCaptureMethodProgress,
  requireCaptureMethodAdapter,
  validateCaptureMethodAdapters,
  type CaptureMethodAdapter,
  type CaptureMethodAdapterContext,
  type CaptureMethodAdapterProgress,
} from "./method-adapter.js";
import {
  applyCaptureExperienceTheme,
  captureExperiencePresentation,
  type CaptureExperiencePresentation,
} from "./experience.js";

export const IDENQA_CAPTURE_TAG_NAME = "idenqa-capture";

export interface CaptureMethodSelectDetail {
  readonly requirementKey: string;
  readonly evidenceType: string;
  readonly artefact: string;
  readonly method: string;
  readonly fallbackCondition?: CaptureRuntimeFallbackReason;
}

export interface CaptureSubjectResponseDetail {
  readonly action: SubjectResponseAction;
  readonly responseId: string;
  readonly authorityId: string;
  readonly noticeId: string;
}

export interface CaptureFlowErrorDetail {
  readonly code: "CAPTURE_FLOW_REQUEST_FAILED";
}

export interface CaptureRealtimeDetail {
  readonly event: CaptureRealtimeEvent;
}

export interface CaptureEvidenceAcceptedDetail {
  readonly uploadId: string;
  readonly evidenceId: string;
  readonly requirementKey: string;
  readonly artefact: string;
  readonly acquisitionMethod: string;
}

export interface CaptureProgressDetail {
  readonly verificationId: string;
  readonly completedSteps: number;
  readonly totalSteps: number;
}

export interface CaptureElementStartOptions extends CaptureFlowStartOptions {
  readonly expectedVerificationId?: string;
  /** Optional translations for package-owned UI copy. The server notice is never overridden. */
  readonly messageCatalogue?: CaptureMessageCatalogue;
  /**
   * Optional signed portable-experience resolution. It supplies the pinned
   * locale, tenant copy for the allow-listed UI keys, safe theme tokens where
   * the host has not set its own, and validated links. It never changes
   * assurance, notices, or capture requirements.
   */
  readonly experience?: ExperienceResolution;
  /** Programmatic integrations for approved acquisition methods not owned by the built-in UI. */
  readonly methodAdapters?: readonly CaptureMethodAdapter[];
  /**
   * Optional tuning for automatic document capture, perspective correction, and
   * cropping. Detection is enabled for document artefacts unless explicitly disabled.
   */
  readonly documentCapture?: CaptureDocumentCaptureOptions;
}

export interface CaptureCompleteDetail extends CaptureProgressDetail {
  readonly captureComplete: true;
}

interface StepUploadState {
  readonly status: "reviewing" | "uploading" | "error" | "accepted";
  readonly body?: Blob;
  readonly previewUrl?: string;
  readonly message?: string;
}

interface StepCameraState {
  readonly status: "requesting" | "streaming" | "reviewing" | "uploading" | "error" | "accepted";
  readonly stream?: MediaStream;
  readonly ready?: boolean;
  readonly body?: Blob;
  readonly previewUrl?: string;
  readonly width?: number;
  readonly height?: number;
  readonly message?: string;
}

interface StepCompletion {
  readonly acquisitionMethod: string;
  readonly uploadId: string;
  readonly evidenceId: string;
}

interface StepDocumentState {
  readonly status: DocumentCaptureGateResult["status"];
  readonly reason?: DocumentCaptureGateResult["reason"];
  readonly progress: number;
  readonly quad?: DocumentQuad;
  readonly frameWidth?: number;
  readonly frameHeight?: number;
}

interface StepDocumentObservation {
  interval: ReturnType<typeof globalThis.setInterval> | undefined;
  autoCaptureTimer: ReturnType<typeof globalThis.setTimeout> | undefined;
  readonly observer: DocumentFrameObserver;
  readonly gate: DocumentCaptureGate;
  detection: DocumentDetection | undefined;
}

interface StepAdapterState {
  readonly status: "running" | "error";
  readonly controller: AbortController;
  readonly progress?: CaptureMethodAdapterProgress;
  readonly previewStream?: MediaStream;
  readonly message?: string;
}

type ComponentFlowState = "idle" | "loading" | "ready" | "responding" | "error" | "cancelled";
type JourneyPhase =
  "intro" | "notice" | "recovery" | "capture" | "confirmation" | "processing" | "complete";
type StepStage = "method" | "preparation" | "capture";

export class IdenqaCaptureElement extends LitElement {
  static override properties = {
    plan: { attribute: false },
  };

  static override styles = css`
    :host {
      --idq-capture-accent: #0b6b57;
      --idq-capture-accent-strong: #075345;
      --idq-capture-accent-foreground: #ffffff;
      --idq-capture-background: #ffffff;
      --idq-capture-border: #d6dedb;
      --idq-capture-card-radius: 0.75rem;
      --idq-capture-control-radius: 0.625rem;
      --idq-capture-error: #b42318;
      --idq-capture-face-guide: rgb(255 255 255 / 78%);
      --idq-capture-face-guide-muted: rgb(255 255 255 / 42%);
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
      --idq-capture-muted: #52605c;
      --idq-capture-overlay-background: rgb(10 18 16 / 82%);
      --idq-capture-overlay-border: rgb(255 255 255 / 18%);
      --idq-capture-overlay-foreground: #ffffff;
      --idq-capture-panel-radius: 1rem;
      --idq-capture-shell-max-width: 42rem;
      --idq-capture-shell-min-height: min(42rem, calc(100dvh - 2rem));
      --idq-capture-shell-padding: 1.25rem;
      --idq-capture-shell-radius: 1.5rem;
      --idq-capture-shell-shadow: 0 1.25rem 4rem rgb(20 32 29 / 10%);
      --idq-capture-surface: #f3f7f5;
      --idq-capture-surface-strong: #e4f2ed;
      --idq-capture-tap-highlight: rgb(23 92 211 / 18%);
      --idq-capture-text: #14201d;
      color-scheme: light dark;
      color: var(--idq-capture-text);
      display: block;
      font-family: var(--idq-capture-font-family);
      line-height: 1.5;
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
      max-width: var(--idq-capture-shell-max-width);
      min-height: var(--idq-capture-shell-min-height);
      overflow-wrap: anywhere;
      padding: max(var(--idq-capture-shell-padding), env(safe-area-inset-top))
        max(var(--idq-capture-shell-padding), env(safe-area-inset-right))
        max(var(--idq-capture-shell-padding), env(safe-area-inset-bottom))
        max(var(--idq-capture-shell-padding), env(safe-area-inset-left));
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
    p {
      margin-block-start: 0;
    }

    h2 {
      font-size: clamp(1.75rem, 6vw, 2.5rem);
      letter-spacing: -0.025em;
      line-height: 1.12;
      margin-block-end: 0.75rem;
      text-wrap: balance;
    }

    h3 {
      font-size: 1.125rem;
      line-height: 1.35;
      margin-block-end: 0.25rem;
      text-wrap: balance;
    }

    h4 {
      font-size: 1rem;
      margin-block-end: 0.75rem;
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
    }

    .notice h4 {
      margin-block: 1rem 0.25rem;
    }

    .notice-copy {
      white-space: pre-wrap;
    }

    .notice-meta {
      display: grid;
      font-size: 0.875rem;
      gap: 0.75rem;
      grid-template-columns: repeat(auto-fit, minmax(min(100%, 12rem), 1fr));
      margin-block: 1.25rem 0;
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
      margin-block: 1.25rem 0;
      padding-inline-start: 0.75rem;
    }

    .error {
      color: var(--idq-capture-error);
      font-weight: 650;
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
      border-radius: var(--idq-capture-panel-radius);
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
      border: 0.2rem solid var(--idq-capture-face-guide);
      border-block-color: var(--idq-capture-face-guide-muted);
      border-radius: 48% 48% 44% 44% / 42% 42% 56% 56%;
      inset: 10% 27% 8%;
      pointer-events: none;
      position: absolute;
    }

    .liveness-overlay-prompt {
      background: var(--idq-capture-overlay-background);
      border: 1px solid var(--idq-capture-overlay-border);
      border-radius: 999px;
      color: var(--idq-capture-overlay-foreground);
      font-size: clamp(0.875rem, 3vw, 1rem);
      font-weight: 750;
      inset-block-start: 1rem;
      inset-inline: 50% auto;
      max-width: calc(100% - 2rem);
      padding: 0.625rem 1rem;
      pointer-events: none;
      position: absolute;
      text-align: center;
      transform: translateX(-50%);
      white-space: nowrap;
    }

    [dir="rtl"] .liveness-overlay-prompt {
      transform: translateX(50%);
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
      background: var(--idq-capture-surface);
      border: 1px solid var(--idq-capture-border);
      border-radius: var(--idq-capture-card-radius);
      display: grid;
      gap: 0.625rem;
      padding: 1rem;
    }

    .adapter-progress p {
      margin: 0;
    }

    .adapter-prompt {
      font-size: clamp(1.125rem, 4vw, 1.4rem);
      font-weight: 750;
      text-wrap: balance;
    }

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
      min-height: 3rem;
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

    .product-header {
      align-items: center;
      display: flex;
      gap: 0.625rem;
      margin-block-end: clamp(2rem, 7vw, 3.5rem);
    }

    .mark {
      align-items: center;
      background: var(--idq-capture-accent);
      border-radius: 0.65rem;
      color: var(--idq-capture-accent-foreground);
      display: inline-flex;
      font-size: 0.875rem;
      font-weight: 800;
      block-size: 2rem;
      inline-size: 2rem;
      justify-content: center;
    }

    .product-name {
      font-size: 0.9375rem;
      font-weight: 750;
      letter-spacing: -0.01em;
      margin: 0;
    }

    .screen {
      display: grid;
      gap: 1.25rem;
      margin-inline: auto;
      max-width: 34rem;
    }

    .screen-copy {
      color: var(--idq-capture-muted);
      font-size: 1rem;
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
      font-size: 1.5rem;
      font-weight: 800;
      block-size: 4rem;
      inline-size: 4rem;
      justify-content: center;
    }

    .liveness-illustration {
      align-items: center;
      align-self: center;
      background:
        radial-gradient(circle at 50% 42%, var(--idq-capture-background) 0 28%, transparent 29%),
        linear-gradient(145deg, var(--idq-capture-surface-strong), var(--idq-capture-surface));
      border: 1px solid var(--idq-capture-border);
      border-radius: 50%;
      color: var(--idq-capture-liveness-color);
      display: flex;
      height: var(--idq-capture-liveness-size);
      justify-content: center;
      justify-self: center;
      overflow: hidden;
      width: var(--idq-capture-liveness-size);
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
      block-size: 3.25rem;
      inline-size: 3.25rem;
    }

    .benefits,
    .tips {
      display: grid;
      gap: 0.875rem;
      list-style: none;
      margin: 0;
      padding: 0;
    }

    .benefits li,
    .tips li {
      align-items: flex-start;
      display: grid;
      gap: 0.75rem;
      grid-template-columns: 1.5rem 1fr;
    }

    .benefits li::before,
    .tips li::before {
      align-items: center;
      background: var(--idq-capture-surface-strong);
      border-radius: 50%;
      color: var(--idq-capture-accent-strong);
      content: "✓";
      display: inline-flex;
      font-size: 0.75rem;
      font-weight: 900;
      block-size: 1.5rem;
      inline-size: 1.5rem;
      justify-content: center;
      margin-block-start: 0.1rem;
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
      margin-block-start: 0.5rem;
    }

    .journey-actions.split {
      grid-template-columns: minmax(0, 1fr) minmax(0, 2fr);
    }

    .journey-progress {
      align-items: center;
      display: grid;
      gap: 0.75rem;
      grid-template-columns: 1fr auto;
      margin-block-end: 1.75rem;
    }

    .journey-progress p {
      color: var(--idq-capture-muted);
      font-size: 0.8125rem;
      font-weight: 700;
      margin: 0;
    }

    .journey-progress progress {
      grid-column: 1 / -1;
      block-size: 0.4rem;
    }

    .method-list {
      display: grid;
      gap: 0.75rem;
    }

    .method-card {
      align-items: center;
      display: grid;
      gap: 0.75rem;
      grid-template-columns: auto 1fr auto;
      justify-content: initial;
      min-height: 4.5rem;
      padding: 1rem;
      text-align: start;
    }

    .method-card .method-icon {
      align-items: center;
      background: var(--idq-capture-surface-strong);
      border-radius: 0.65rem;
      color: var(--idq-capture-accent-strong);
      display: inline-flex;
      block-size: 2.5rem;
      inline-size: 2.5rem;
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

    .capture-panel {
      background: var(--idq-capture-surface);
      border: 1px solid var(--idq-capture-border);
      border-radius: var(--idq-capture-panel-radius);
      display: grid;
      gap: 1rem;
      padding: 1rem;
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
    }

    .confirmation-card strong {
      display: block;
      margin-block-end: 0.125rem;
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

    @keyframes idq-pulse {
      0%,
      100% {
        opacity: 0.25;
        transform: translateY(0);
      }
      50% {
        opacity: 1;
        transform: translateY(-0.2rem);
      }
    }

    @keyframes idq-liveness-head-demo {
      0%,
      23%,
      48%,
      73%,
      100% {
        transform: translate3d(0, 0, 0) rotate(0) scaleX(1);
      }
      5%,
      17% {
        transform: translate3d(-0.35rem, 0, 0) rotate(-3deg) scaleX(0.96);
      }
      30%,
      42% {
        transform: translate3d(0.35rem, 0, 0) rotate(3deg) scaleX(0.96);
      }
      55%,
      67% {
        transform: translate3d(0, -0.35rem, 0) rotate(0) scaleX(1);
      }
      80%,
      92% {
        transform: translate3d(0, 0.35rem, 0) rotate(0) scaleX(1);
      }
    }

    @keyframes idq-liveness-gaze-demo {
      0%,
      23%,
      48%,
      73%,
      100% {
        transform: translate3d(0, 0, 0);
      }
      5%,
      17% {
        transform: translate3d(-0.22rem, 0, 0);
      }
      30%,
      42% {
        transform: translate3d(0.22rem, 0, 0);
      }
      55%,
      67% {
        transform: translate3d(0, -0.18rem, 0);
      }
      80%,
      92% {
        transform: translate3d(0, 0.18rem, 0);
      }
    }

    @keyframes idq-liveness-direction-left-demo {
      0%,
      23%,
      100% {
        opacity: var(--idq-capture-liveness-cue-opacity);
        transform: translate3d(0, 0, 0);
      }
      5%,
      17% {
        opacity: var(--idq-capture-liveness-cue-active-opacity);
        transform: translate3d(var(--idq-liveness-cue-x), var(--idq-liveness-cue-y), 0);
      }
    }

    @keyframes idq-liveness-direction-right-demo {
      0%,
      23%,
      48%,
      100% {
        opacity: var(--idq-capture-liveness-cue-opacity);
        transform: translate3d(0, 0, 0);
      }
      30%,
      42% {
        opacity: var(--idq-capture-liveness-cue-active-opacity);
        transform: translate3d(var(--idq-liveness-cue-x), var(--idq-liveness-cue-y), 0);
      }
    }

    @keyframes idq-liveness-direction-up-demo {
      0%,
      48%,
      73%,
      100% {
        opacity: var(--idq-capture-liveness-cue-opacity);
        transform: translate3d(0, 0, 0);
      }
      55%,
      67% {
        opacity: var(--idq-capture-liveness-cue-active-opacity);
        transform: translate3d(var(--idq-liveness-cue-x), var(--idq-liveness-cue-y), 0);
      }
    }

    @keyframes idq-liveness-direction-down-demo {
      0%,
      73%,
      100% {
        opacity: var(--idq-capture-liveness-cue-opacity);
        transform: translate3d(0, 0, 0);
      }
      80%,
      92% {
        opacity: var(--idq-capture-liveness-cue-active-opacity);
        transform: translate3d(var(--idq-liveness-cue-x), var(--idq-liveness-cue-y), 0);
      }
    }

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
      button {
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
        min-height: 100dvh;
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

    @media (forced-colors: active) {
      button[aria-pressed="true"] {
        outline: 0.1875rem solid ButtonText;
      }
    }
  `;

  declare plan: CapturePlan | undefined;

  /** Signed portable-experience presentation applied to this journey, if any. */
  get experience(): CaptureExperiencePresentation | undefined {
    return this.#experience;
  }

  readonly #selectedMethods = new Map<string, string>();
  readonly #uploadStates = new Map<string, StepUploadState>();
  readonly #cameraStates = new Map<string, StepCameraState>();
  readonly #adapterStates = new Map<string, StepAdapterState>();
  readonly #completedSteps = new Map<string, StepCompletion>();
  #methodAdapters: readonly CaptureMethodAdapter[] = [];
  #flowController: CaptureFlowController | undefined;
  #flowSnapshot: CaptureFlowSnapshot | undefined;
  #flowState: ComponentFlowState = "idle";
  #flowAbortController: AbortController | undefined;
  #responseError = false;
  #captureCompleteDispatched = false;
  #outcomePolling = false;
  #localizer: CaptureLocalizer = createCaptureLocalizer(browserLocale());
  #experience: CaptureExperiencePresentation | undefined;
  #journeyPhase: JourneyPhase = "intro";
  #stepStage: StepStage = "method";
  #activeStepIndex = 0;
  readonly #documentStates = new Map<string, StepDocumentState>();
  readonly #documentObservations = new Map<string, StepDocumentObservation>();
  readonly #capturingSteps = new Set<string>();
  #documentCapture: NormalizedCaptureDocumentCaptureOptions =
    normalizeCaptureDocumentCaptureOptions();

  async start(options: CaptureElementStartOptions): Promise<CaptureFlowSnapshot> {
    const {
      messageCatalogue = {},
      experience,
      expectedVerificationId,
      methodAdapters = [],
      documentCapture,
      ...flowOptions
    } = options;
    const presentation =
      experience === undefined ? undefined : captureExperiencePresentation(experience);
    this.#experience = presentation;
    if (presentation?.theme !== undefined) applyCaptureExperienceTheme(this, presentation.theme);
    const catalogue = mergeCaptureMessageCatalogues(
      presentation?.messageCatalogue,
      messageCatalogue,
    );
    const documentCaptureOptions = normalizeCaptureDocumentCaptureOptions(documentCapture);
    this.#localizer = createCaptureLocalizer(presentation?.locale ?? browserLocale(), catalogue);
    this.#documentCapture = documentCaptureOptions;
    this.#clearCameraStates();
    this.#clearAdapterStates();
    this.#flowAbortController?.abort();
    const abortController = new AbortController();
    this.#flowAbortController = abortController;
    this.#flowController = createCaptureFlowController(flowOptions);
    this.#methodAdapters = [];
    this.#flowSnapshot = undefined;
    this.#flowState = "loading";
    this.#journeyPhase = "intro";
    this.#stepStage = "method";
    this.#activeStepIndex = 0;
    this.#responseError = false;
    this.#clearUploadStates();
    this.#cameraStates.clear();
    this.#completedSteps.clear();
    this.#captureCompleteDispatched = false;
    this.#outcomePolling = false;
    this.plan = undefined;
    this.requestUpdate();
    try {
      this.#methodAdapters = validateCaptureMethodAdapters(methodAdapters);
      const snapshot = await this.#flowController.load(abortController.signal);
      if (
        expectedVerificationId !== undefined &&
        snapshot.outcome.verificationId !== expectedVerificationId
      )
        throw new Error("Capture credential does not match the expected linked session.");
      if (this.#flowAbortController !== abortController) return snapshot;
      this.#flowSnapshot = snapshot;
      this.#flowState = "ready";
      if (!isActiveCaptureFlowSnapshot(snapshot)) {
        if (snapshot.status === "processing" || snapshot.status === "action_required") {
          this.#pollAuthoritativeOutcome();
        } else {
          this.#dropFlowController();
        }
      } else {
        this.#localizer = createCaptureLocalizer(
          this.#experience?.locale ?? snapshot.authoritySnapshot.notice.locale,
          catalogue,
        );
        this.#assertMethodAdapters(snapshot.plan);
        this.#restoreRecoveredProgress(snapshot);
        if (snapshot.status === "authority_blocked" || snapshot.status === "refused") {
          this.#dropFlowController();
          this.requestUpdate();
          return snapshot;
        }
        void this.#flowController
          .observe(
            (event) => this.#dispatchRealtimeEvent(event),
            (recovered) => {
              if (abortController.signal.aborted) return;
              this.#flowSnapshot = recovered;
              if (isActiveCaptureFlowSnapshot(recovered)) {
                this.#restoreRecoveredProgress(recovered);
              } else if (
                recovered.status === "processing" ||
                recovered.status === "action_required"
              ) {
                this.#pollAuthoritativeOutcome();
              } else {
                this.#dropFlowController();
              }
              this.requestUpdate();
            },
            abortController.signal,
          )
          .catch(() => {
            if (!abortController.signal.aborted) this.#dispatchFlowError();
          });
      }
      this.requestUpdate();
      return snapshot;
    } catch (error) {
      if (this.#flowAbortController !== abortController) throw error;
      const wasAborted = abortController.signal.aborted;
      this.#flowState = wasAborted ? "cancelled" : "error";
      this.#dropFlowController();
      this.requestUpdate();
      if (!wasAborted) this.#dispatchFlowError();
      throw error;
    }
  }

  cancel(): void {
    this.#dropFlowController();
    this.#flowSnapshot = undefined;
    this.#flowState = "cancelled";
    this.#journeyPhase = "intro";
    this.#responseError = false;
    this.#clearUploadStates();
    this.#clearCameraStates();
    this.#clearAdapterStates();
    this.#methodAdapters = [];
    this.#completedSteps.clear();
    this.#captureCompleteDispatched = false;
    this.requestUpdate();
  }

  override disconnectedCallback(): void {
    this.cancel();
    super.disconnectedCallback();
  }

  protected override willUpdate(changedProperties: PropertyValues<this>): void {
    if (changedProperties.has("plan")) {
      this.#selectedMethods.clear();
      this.#clearUploadStates();
      this.#clearCameraStates();
      this.#clearAdapterStates();
      this.#completedSteps.clear();
      this.#captureCompleteDispatched = false;
    }
  }

  protected override render() {
    return html`
      <section
        class="shell"
        aria-labelledby="capture-title"
        lang=${this.#localizer.locale}
        dir=${this.#localizer.direction}
      >
        <header class="product-header">
          <span class="mark" aria-hidden="true">I</span>
          <h2 class="product-name" id="capture-title">${this.#text("identityVerification")}</h2>
        </header>
        ${
          this.#flowState === "loading"
            ? this.#renderLoading()
            : this.#flowState === "error"
              ? this.#renderError()
              : this.#flowState === "cancelled"
                ? this.#renderCancelled()
                : this.#flowSnapshot !== undefined
                  ? this.#renderFlow(this.#flowSnapshot)
                  : this.plan === undefined
                    ? this.#renderLoading(false)
                    : this.#renderPlan(this.plan)
        }
      </section>
    `;
  }

  #renderFlow(flow: CaptureFlowSnapshot) {
    if (!isActiveCaptureFlowSnapshot(flow)) return this.#renderAuthoritativeOutcome(flow);
    if (this.#journeyPhase === "intro") return this.#renderIntroduction(flow);
    if (flow.status === "refused") return this.#renderRefused();
    if (flow.status === "authority_blocked") return this.#renderAuthorityBlocked();
    if (this.#journeyPhase === "notice" || flow.status === "notice_required") {
      return this.#renderNotice(flow);
    }
    if (this.#journeyPhase === "recovery") return this.#renderRecovery(flow.plan);
    if (this.#journeyPhase === "confirmation") return this.#renderConfirmation(flow.plan);
    if (this.#journeyPhase === "processing") return this.#renderProcessing();
    if (this.#journeyPhase === "complete") return this.#renderComplete();
    return this.#renderGuidedPlan(flow.plan, flow.authoritySnapshot.notice.locale);
  }

  #renderLoading(secure = true) {
    return html`
      <div class="screen" role="status" aria-live="polite" aria-busy="true">
        <span class="state-icon" aria-hidden="true">…</span>
        <div>
          <h2>${secure ? this.#text("preparingSecureCapture") : this.#text("preparingCapture")}</h2>
          <p class="screen-copy">${this.#text("preparingCaptureBody")}</p>
        </div>
        <div class="processing-indicator" aria-hidden="true">
          <span></span><span></span><span></span>
        </div>
      </div>
    `;
  }

  #renderError() {
    return html`
      <div class="screen">
        <span class="state-icon" aria-hidden="true">!</span>
        <div>
          <h2>${this.#text("capturePreparationFailedTitle")}</h2>
          <p class="screen-copy" role="alert">${this.#text("capturePreparationFailed")}</p>
        </div>
      </div>
    `;
  }

  #renderCancelled() {
    return html`
      <div class="screen">
        <span class="state-icon" aria-hidden="true">×</span>
        <div>
          <h2>${this.#text("captureCancelledTitle")}</h2>
          <p class="screen-copy" role="status" aria-live="polite">
            ${this.#text("captureCancelled")}
          </p>
        </div>
      </div>
    `;
  }

  #renderIntroduction(flow: CaptureActiveFlowSnapshot) {
    const totalSteps = planSteps(flow.plan).length;
    return html`
      <div class="screen">
        <span class="hero-icon" aria-hidden="true">✓</span>
        <div>
          <p class="eyebrow">${this.#text("secureCapture")}</p>
          <h2>${this.#text("introTitle")}</h2>
          <p class="screen-copy">${this.#text("introBody")}</p>
        </div>
        <ul class="benefits">
          <li>
            ${this.#text("introStepCount", { count: formatNumber(totalSteps, this.#localizer.locale) })}
          </li>
          <li>${this.#text("introPrivate")}</li>
          <li>${this.#text("introDevice")}</li>
        </ul>
        <div class="journey-actions">
          <button class="primary" type="button" @click=${() => this.#beginJourney(flow)}>
            ${this.#text("getStarted")}
          </button>
          <button class="quiet" type="button" @click=${() => this.cancel()}>
            ${this.#text("cancel")}
          </button>
        </div>
      </div>
    `;
  }

  #renderNotice(flow: CaptureActiveFlowSnapshot) {
    const { authority, notice, latestResponse } = flow.authoritySnapshot;
    return html`
      <div class="screen notice-screen">
        <div>
          <p class="eyebrow">${this.#text("noticeStep")}</p>
          <h2>${this.#text("noticeTitle")}</h2>
          <p class="screen-copy">${this.#text("noticeBody")}</p>
        </div>
        <article class="notice" aria-labelledby="idq-notice-title" lang=${notice.locale}>
          <h3 id="idq-notice-title">${notice.copy.title}</h3>
          <p class="notice-copy">${notice.copy.summary}</p>
          <h4>${this.#text("whyInformationNeeded")}</h4>
          <p class="notice-copy">${notice.copy.purpose}</p>
          <h4>${this.#text("ifYouRefuse")}</h4>
          <p class="notice-copy">${notice.copy.consequences}</p>
          <dl class="notice-meta">
            <div>
              <dt>${this.#text("controller")}</dt>
              <dd>${notice.controller}</dd>
            </div>
            <div>
              <dt>${this.#text("recipient")}</dt>
              <dd>${notice.recipient}</dd>
            </div>
          </dl>
          ${
            flow.status === "notice_required"
              ? this.#renderNoticeActions(authority.consentRequired)
              : html`<p class="notice-guidance" role="status">
                  ${
                    latestResponse?.action === "consent"
                      ? this.#text("consentRecorded")
                      : this.#text("acknowledgementRecorded")
                  }
                </p>`
          }
        </article>
        <button class="quiet" type="button" @click=${() => this.cancel()}>
          ${this.#text("cancel")}
        </button>
      </div>
    `;
  }

  #renderNoticeActions(consentRequired: boolean) {
    const busy = this.#flowState === "responding";
    const acceptedAction: SubjectResponseAction = consentRequired ? "consent" : "acknowledge";
    return html`
      <p class="notice-guidance">
        ${consentRequired ? this.#text("reviewConsent") : this.#text("reviewAcknowledgement")}
      </p>
      <div class="notice-actions" role="group" aria-label=${this.#text("noticeActionsLabel")}>
        <button
          class="primary"
          type="button"
          ?disabled=${busy}
          @click=${() => void this.#respond(acceptedAction)}
        >
          ${
            busy
              ? this.#text("recordingResponse")
              : consentRequired
                ? this.#text("agreeAndContinue")
                : this.#text("acknowledgeAndContinue")
          }
        </button>
        <button type="button" ?disabled=${busy} @click=${() => void this.#respond("refuse")}>
          ${this.#text("refuse")}
        </button>
      </div>
      ${
        this.#responseError
          ? html`<p class="error" role="alert">${this.#text("responseFailed")}</p>`
          : nothing
      }
    `;
  }

  #renderGuidedPlan(plan: CapturePlan, locale = this.#localizer.locale) {
    const steps = planSteps(plan);
    const step = steps[this.#activeStepIndex];
    if (step === undefined) return this.#renderProcessing();
    const completedSteps = steps.filter((candidate) =>
      this.#completedSteps.has(stepKey(candidate)),
    ).length;
    const item = friendlyArtefact(step.artefact, this.#localizer);
    return html`
      <div class="screen">
        <div class="journey-progress">
          <p>
            ${this.#text("stepOf", {
              current: formatNumber(this.#activeStepIndex + 1, locale),
              total: formatNumber(steps.length, locale),
            })}
          </p>
          <p>
            ${this.#text("progressPercent", { percent: formatNumber(Math.round((completedSteps / steps.length) * 100), locale) })}
          </p>
          <progress
            aria-label=${this.#text("progressLabel")}
            value=${completedSteps}
            max=${steps.length}
          ></progress>
        </div>
        ${
          step.fallbackCondition === undefined
            ? nothing
            : html`<p class="fallback" role="status">
                ${fallbackMessage(step.fallbackCondition, this.#localizer)}
              </p>`
        }
        ${
          this.#stepStage === "method"
            ? this.#renderMethodChoice(step, item)
            : this.#stepStage === "preparation"
              ? this.#renderPreparation(step, item)
              : this.#renderCaptureTask(step, item)
        }
      </div>
    `;
  }

  #renderMethodChoice(step: CapturePlanStep, item: string) {
    return html`
      <div>
        <p class="eyebrow">${this.#text("chooseMethod")}</p>
        <h2>${this.#text("chooseMethodTitle", { item })}</h2>
        <p class="screen-copy">${this.#text("chooseMethodBody")}</p>
      </div>
      <div
        class="method-list"
        role="group"
        aria-label=${this.#text("captureMethodsLabel", { artefact: item })}
      >
        ${step.methodOptions.map((method) => {
          const adapter = this.#adapterFor(step, method);
          const copy =
            adapter === undefined
              ? undefined
              : captureMethodAdapterCopy(adapter, this.#localizer.locale);
          return html`
            <button
              class="method-card"
              type="button"
              @click=${() => this.#chooseGuidedMethod(step, method)}
            >
              <span class="method-icon" aria-hidden="true">
                ${adapter !== undefined ? "◎" : method === LIVE_CAMERA_METHOD ? "◉" : method === FILE_UPLOAD_METHOD ? "↑" : "→"}
              </span>
              <span class="method-copy">
                <strong>${copy?.action ?? methodAction(method, this.#localizer)}</strong>
                <small>${copy?.description ?? methodDescription(method, this.#localizer)}</small>
              </span>
              <span class="chevron" aria-hidden="true">›</span>
            </button>
          `;
        })}
      </div>
      ${this.#renderJourneyExit()}
    `;
  }

  #renderPreparation(step: CapturePlanStep, item: string) {
    const method = this.#selectedMethods.get(stepKey(step)) ?? step.methodOptions[0]!;
    const adapter = this.#adapterFor(step, method);
    const copy =
      adapter === undefined ? undefined : captureMethodAdapterCopy(adapter, this.#localizer.locale);
    const activeLiveness = adapter?.presentation === "active_liveness";
    return html`
      ${
        activeLiveness
          ? this.#renderLivenessIllustration()
          : html`<span class="hero-icon" aria-hidden="true"
              >${adapter !== undefined ? "◎" : method === LIVE_CAMERA_METHOD ? "◉" : "↑"}</span
            >`
      }
      <div>
        <p class="eyebrow">${this.#text("prepare")}</p>
        <h2>${copy?.title ?? this.#text("prepareTitle", { item })}</h2>
        <p class="screen-copy">
          ${copy?.preparation ?? (method === LIVE_CAMERA_METHOD ? this.#text("cameraPreparation") : this.#text("filePreparation"))}
        </p>
      </div>
      ${
        copy?.tips === undefined
          ? html`<ul class="tips">
              <li>${this.#text("tipLighting")}</li>
              <li>${this.#text("tipReadable")}</li>
              <li>${this.#text("tipPrivacy")}</li>
            </ul>`
          : copy.tips.length === 0
            ? nothing
            : html`<ul class="tips">
                ${copy.tips.map((tip) => html`<li>${tip}</li>`)}
              </ul>`
      }
      <div class="journey-actions">
        <button
          class="primary"
          type="button"
          @click=${() =>
            adapter === undefined
              ? this.#showCaptureTask()
              : void this.#startGuidedAdapter(step, adapter)}
        >
          ${copy?.action ?? this.#text("continue")}
        </button>
        <button type="button" @click=${() => this.#backFromPreparation(step)}>
          ${step.methodOptions.length > 1 ? this.#text("chooseAnotherMethod") : this.#text("back")}
        </button>
      </div>
      <button class="quiet" type="button" @click=${() => this.cancel()}>
        ${this.#text("cancel")}
      </button>
    `;
  }

  #renderLivenessIllustration() {
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
          <path
            class="liveness-direction liveness-direction-right"
            d="m130 73 7 7-7 7M137 80h-12"
          />
          <path class="liveness-direction liveness-direction-up" d="m73 30 7-7 7 7M80 23v12" />
          <path class="liveness-direction liveness-direction-down" d="m73 130 7 7 7-7M80 137v-12" />
        </svg>
      </div>
    `;
  }

  #renderCaptureTask(step: CapturePlanStep, item: string) {
    const key = stepKey(step);
    const method = this.#selectedMethods.get(key) ?? step.methodOptions[0]!;
    const uploadState = this.#uploadStates.get(key);
    const cameraState = this.#cameraStates.get(key);
    const adapter = this.#adapterFor(step, method);
    const adapterState = this.#adapterStates.get(key);
    const copy =
      adapter === undefined ? undefined : captureMethodAdapterCopy(adapter, this.#localizer.locale);
    const busy =
      uploadState?.status === "uploading" ||
      isCameraBusy(cameraState) ||
      adapterState?.status === "running";
    return html`
      <div>
        <p class="eyebrow">${this.#text("capture")}</p>
        <h2>${this.#text("captureTitle", { item })}</h2>
        <p class="screen-copy">
          ${copy?.instruction ?? captureInstruction(method, this.#localizer)}
        </p>
      </div>
      <div class="capture-panel">
        ${
          adapter !== undefined
            ? this.#renderMethodAdapter(
                step,
                `guided-${this.#activeStepIndex}`,
                adapter,
                adapterState,
              )
            : method === FILE_UPLOAD_METHOD
              ? this.#renderFileUpload(step, `guided-${this.#activeStepIndex}`, uploadState, false)
              : method === LIVE_CAMERA_METHOD
                ? this.#renderCamera(step, `guided-${this.#activeStepIndex}`, cameraState, false)
                : html`<button
                    class="primary"
                    type="button"
                    @click=${() => this.#selectMethod(step, method)}
                  >
                    ${methodAction(method, this.#localizer)}
                  </button>`
        }
      </div>
      <div class="journey-actions">
        <button type="button" ?disabled=${busy} @click=${() => this.#backToPreparation(step)}>
          ${this.#text("back")}
        </button>
        <button class="quiet" type="button" ?disabled=${busy} @click=${() => this.cancel()}>
          ${this.#text("cancel")}
        </button>
      </div>
    `;
  }

  #renderRecovery(plan: CapturePlan) {
    const progress = captureProgress(plan, this.#completedSteps);
    return html`
      <div class="screen">
        <span class="hero-icon" aria-hidden="true">↻</span>
        <div>
          <p class="eyebrow">${this.#text("recovery")}</p>
          <h2>${this.#text("recoveryTitle")}</h2>
          <p class="screen-copy">
            ${this.#text("recoveryBody", {
              completed: formatNumber(progress.completedSteps, this.#localizer.locale),
              total: formatNumber(progress.totalSteps, this.#localizer.locale),
            })}
          </p>
        </div>
        <div class="confirmation-card">
          <span class="state-icon" aria-hidden="true">✓</span>
          <p><strong>${this.#text("savedProgress")}</strong>${this.#text("savedProgressBody")}</p>
        </div>
        <div class="journey-actions">
          <button class="primary" type="button" @click=${() => this.#resumeJourney(plan)}>
            ${progress.completedSteps === progress.totalSteps ? this.#text("reviewAndFinish") : this.#text("resumeCapture")}
          </button>
          <button class="quiet" type="button" @click=${() => this.cancel()}>
            ${this.#text("cancel")}
          </button>
        </div>
      </div>
    `;
  }

  #renderConfirmation(plan: CapturePlan) {
    const steps = planSteps(plan);
    const step = steps[this.#activeStepIndex];
    const item =
      step === undefined
        ? this.#text("requiredItem")
        : friendlyArtefact(step.artefact, this.#localizer);
    const isLast = steps.every((candidate) => this.#completedSteps.has(stepKey(candidate)));
    const completedSteps = steps.filter((candidate) =>
      this.#completedSteps.has(stepKey(candidate)),
    ).length;
    return html`
      <div class="screen" aria-live="polite">
        <div class="journey-progress">
          <p>
            ${this.#text("stepOf", {
              current: formatNumber(
                Math.min(this.#activeStepIndex + 1, steps.length),
                this.#localizer.locale,
              ),
              total: formatNumber(steps.length, this.#localizer.locale),
            })}
          </p>
          <p>
            ${this.#text("progressPercent", {
              percent: formatNumber(
                Math.round((completedSteps / steps.length) * 100),
                this.#localizer.locale,
              ),
            })}
          </p>
          <progress
            aria-label=${this.#text("progressLabel")}
            value=${completedSteps}
            max=${steps.length}
          ></progress>
        </div>
        <span class="hero-icon" aria-hidden="true">✓</span>
        <div>
          <p class="eyebrow">${this.#text("saved")}</p>
          <h2>${this.#text("confirmationTitle", { item })}</h2>
          <p class="screen-copy">${this.#text("confirmationBody")}</p>
        </div>
        <div class="confirmation-card">
          <span class="state-icon" aria-hidden="true">✓</span>
          <p><strong>${item}</strong>${this.#text("securelyUploaded")}</p>
        </div>
        <div class="journey-actions">
          <button
            class="primary"
            type="button"
            @click=${() => this.#continueAfterConfirmation(plan)}
          >
            ${isLast ? this.#text("reviewAndFinish") : this.#text("nextItem")}
          </button>
          <button class="quiet" type="button" @click=${() => this.cancel()}>
            ${this.#text("cancel")}
          </button>
        </div>
      </div>
    `;
  }

  #renderProcessing() {
    return html`
      <div class="screen" aria-busy="true">
        <span class="state-icon" aria-hidden="true">…</span>
        <div role="status" aria-live="polite" aria-atomic="true">
          <p class="eyebrow">${this.#text("processing")}</p>
          <h2>${this.#text("processingTitle")}</h2>
          <p class="screen-copy">${this.#text("processingBody")}</p>
        </div>
        <div class="processing-indicator" aria-hidden="true">
          <span></span><span></span><span></span>
        </div>
      </div>
    `;
  }

  #renderAuthoritativeOutcome(flow: CaptureOutcomeFlowSnapshot) {
    switch (flow.status) {
      case "processing":
        return this.#renderProcessing();
      case "verified":
        return this.#renderOutcomeScreen(
          "✓",
          this.#text("verificationSuccess"),
          this.#text("verificationSuccessTitle"),
          this.#text("verificationSuccessBody"),
        );
      case "action_required":
        return this.#renderOutcomeScreen(
          "!",
          this.#text("actionRequired"),
          this.#text("actionRequiredTitle"),
          this.#text("actionRequiredBody"),
        );
      case "not_verified":
        return this.#renderOutcomeScreen(
          "×",
          this.#text("verificationUnsuccessful"),
          this.#text("verificationUnsuccessfulTitle"),
          this.#text("verificationUnsuccessfulBody"),
        );
      case "inconclusive":
        return this.#renderOutcomeScreen(
          "!",
          this.#text("verificationUnsuccessful"),
          this.#text("verificationInconclusiveTitle"),
          this.#text("verificationInconclusiveBody"),
        );
      case "expired":
        return this.#renderOutcomeScreen(
          "!",
          this.#text("complete"),
          this.#text("verificationExpiredTitle"),
          this.#text("verificationExpiredBody"),
        );
      case "failed":
        return this.#renderOutcomeScreen(
          "!",
          this.#text("complete"),
          this.#text("verificationFailedTitle"),
          this.#text("verificationFailedBody"),
        );
      case "cancelled":
        return this.#renderOutcomeScreen(
          "×",
          this.#text("complete"),
          this.#text("captureCancelledTitle"),
          this.#text("verificationCancelledBody"),
        );
    }
  }

  #renderOutcomeScreen(icon: string, eyebrow: string, title: string, body: string) {
    return html`
      <div class="screen">
        <span class="hero-icon" aria-hidden="true">${icon}</span>
        <div role="status" aria-live="polite" aria-atomic="true">
          <p class="eyebrow">${eyebrow}</p>
          <h2>${title}</h2>
          <p class="screen-copy">${body}</p>
        </div>
        <p class="privacy-note">${this.#text("closePage")}</p>
      </div>
    `;
  }

  #renderComplete() {
    return html`
      <div class="screen">
        <span class="hero-icon" aria-hidden="true">✓</span>
        <div role="status" aria-live="polite">
          <p class="eyebrow">${this.#text("complete")}</p>
          <h2>${this.#text("completeTitle")}</h2>
          <p class="screen-copy">${this.#text("completeBody")}</p>
        </div>
        <p class="privacy-note">${this.#text("closePage")}</p>
      </div>
    `;
  }

  #renderRefused() {
    return html`
      <div class="screen">
        <span class="state-icon" aria-hidden="true">×</span>
        <div>
          <h2>${this.#text("refusalTitle")}</h2>
          <p class="screen-copy" role="status">${this.#text("refusalRecorded")}</p>
        </div>
      </div>
    `;
  }

  #renderAuthorityBlocked() {
    return html`
      <div class="screen">
        <span class="state-icon" aria-hidden="true">!</span>
        <div>
          <h2>${this.#text("authorityBlockedTitle")}</h2>
          <p class="screen-copy error" role="alert">${this.#text("authorityBlocked")}</p>
        </div>
      </div>
    `;
  }

  #renderJourneyExit() {
    return html`<button class="quiet" type="button" @click=${() => this.cancel()}>
      ${this.#text("cancel")}
    </button>`;
  }

  #beginJourney(flow: CaptureActiveFlowSnapshot): void {
    if (flow.status === "notice_required") {
      this.#journeyPhase = "notice";
    } else if (flow.status === "capture_ready") {
      this.#resumeJourney(flow.plan, true);
    } else if (flow.status === "refused") {
      this.#journeyPhase = "complete";
    }
    this.requestUpdate();
  }

  #resumeJourney(plan: CapturePlan, showRecovery = false): void {
    const progress = captureProgress(plan, this.#completedSteps);
    if (showRecovery && progress.completedSteps > 0) {
      this.#journeyPhase = "recovery";
      this.requestUpdate();
      return;
    }
    if (progress.completedSteps === progress.totalSteps) {
      this.#journeyPhase = "processing";
      this.#pollAuthoritativeOutcome();
      this.requestUpdate();
      return;
    }
    this.#enterNextIncompleteStep(plan, 0);
  }

  #enterNextIncompleteStep(plan: CapturePlan, startIndex: number): void {
    const steps = planSteps(plan);
    const relativeIndex = steps
      .slice(startIndex)
      .findIndex((step) => !this.#completedSteps.has(stepKey(step)));
    if (relativeIndex < 0) {
      this.#journeyPhase = "processing";
      this.#pollAuthoritativeOutcome();
      this.requestUpdate();
      return;
    }
    this.#activeStepIndex = startIndex + relativeIndex;
    const step = steps[this.#activeStepIndex]!;
    const key = stepKey(step);
    this.#selectedMethods.set(key, step.methodOptions[0]!);
    this.#stepStage = "preparation";
    this.#journeyPhase = "capture";
    this.requestUpdate();
  }

  #chooseGuidedMethod(step: CapturePlanStep, method: string): void {
    this.#selectMethod(step, method);
    this.#stepStage = "preparation";
    this.requestUpdate();
  }

  #backFromPreparation(step: CapturePlanStep): void {
    if (step.methodOptions.length > 1) {
      this.#selectedMethods.delete(stepKey(step));
      this.#stepStage = "method";
    } else {
      this.#journeyPhase = "intro";
    }
    this.requestUpdate();
  }

  #showCaptureTask(): void {
    this.#stepStage = "capture";
    this.requestUpdate();
  }

  async #startGuidedAdapter(step: CapturePlanStep, adapter: CaptureMethodAdapter): Promise<void> {
    this.#stepStage = "capture";
    this.requestUpdate();
    await this.updateComplete;
    await this.#runMethodAdapter(
      step,
      adapter,
      `idq-adapter-preview-guided-${this.#activeStepIndex}`,
    );
  }

  #backToPreparation(step: CapturePlanStep): void {
    const key = stepKey(step);
    this.#clearUploadState(key);
    this.#clearCameraState(key);
    this.#clearAdapterState(key);
    this.#stepStage = "preparation";
    this.requestUpdate();
  }

  #continueAfterConfirmation(plan: CapturePlan): void {
    this.#enterNextIncompleteStep(plan, this.#activeStepIndex + 1);
  }

  #renderPlan(plan: CapturePlan, locale = this.#localizer.locale) {
    const steps = plan.requirements.flatMap((requirement) => requirement.steps);
    const completedSteps = steps.filter((step) => this.#completedSteps.has(stepKey(step))).length;
    const totalSteps = steps.length;
    return html`
      <p class="intro">${this.#text("chooseApprovedMethod")}</p>
      <section class="progress-summary" aria-labelledby="idq-progress-copy">
        <p class="progress-copy" id="idq-progress-copy">
          ${this.#text("progressSummary", {
            completed: formatNumber(completedSteps, locale),
            total: formatNumber(totalSteps, locale),
          })}
        </p>
        <progress
          aria-label=${this.#text("progressLabel")}
          value=${completedSteps}
          max=${totalSteps}
        ></progress>
        ${
          completedSteps === totalSteps
            ? html`<p class="completion-copy" role="status" aria-live="polite">
                ${this.#text("captureComplete")}
              </p>`
            : nothing
        }
      </section>
      <div class="requirements">
        ${plan.requirements.map((requirement, requirementIndex) => {
          const requirementCompleted = requirement.steps.filter((step) =>
            this.#completedSteps.has(stepKey(step)),
          ).length;
          return html`
            <section class="requirement" aria-labelledby=${`idq-requirement-${requirementIndex}`}>
              <h3 id=${`idq-requirement-${requirementIndex}`}>
                ${labelForIdentifier(requirement.evidenceType)}
              </h3>
              <p class="requirement-copy">
                ${this.#text("requirementProgress", {
                  completed: formatNumber(requirementCompleted, locale),
                  total: formatNumber(requirement.steps.length, locale),
                })}
              </p>
              <div class="steps">
                ${requirement.steps.map((step, stepIndex) =>
                  this.#renderStep(step, `${requirementIndex}-${stepIndex}`),
                )}
              </div>
            </section>
          `;
        })}
      </div>
    `;
  }

  #renderStep(step: CapturePlanStep, idSuffix: string) {
    const key = stepKey(step);
    const selectedMethod = this.#selectedMethods.get(key);
    const uploadState = this.#uploadStates.get(key);
    const cameraState = this.#cameraStates.get(key);
    const adapterState = this.#adapterStates.get(key);
    const completion = this.#completedSteps.get(key);
    const completionAdapter =
      completion === undefined ? undefined : this.#adapterFor(step, completion.acquisitionMethod);
    const cameraActive = isCameraActive(cameraState);
    const fileActive = uploadState?.status === "uploading";
    const adapterActive = adapterState?.status === "running";
    return html`
      <section
        class=${completion === undefined ? "step" : "step step-complete"}
        aria-labelledby=${`idq-step-${idSuffix}`}
      >
        <h4 id=${`idq-step-${idSuffix}`}>${labelForIdentifier(step.artefact)}</h4>
        ${
          step.fallbackCondition === undefined
            ? nothing
            : html`<p class="fallback">
                ${fallbackMessage(step.fallbackCondition, this.#localizer)}
              </p>`
        }
        ${
          completion === undefined
            ? html`<div
                class="methods"
                role="group"
                aria-label=${this.#text("captureMethodsLabel", {
                  artefact: labelForIdentifier(step.artefact),
                })}
              >
                ${step.methodOptions.map((method) => {
                  const adapter = this.#adapterFor(step, method);
                  if (adapter !== undefined && this.#flowController !== undefined) {
                    return this.#renderMethodAdapter(step, idSuffix, adapter, adapterState);
                  }
                  if (method === FILE_UPLOAD_METHOD && this.#flowController !== undefined) {
                    return this.#renderFileUpload(
                      step,
                      idSuffix,
                      uploadState,
                      cameraActive || adapterActive,
                    );
                  }
                  if (method === LIVE_CAMERA_METHOD && this.#flowController !== undefined) {
                    return this.#renderCamera(
                      step,
                      idSuffix,
                      cameraState,
                      fileActive || adapterActive,
                    );
                  }
                  return html`
                    <button
                      type="button"
                      aria-pressed=${String(selectedMethod === method)}
                      @click=${() => this.#selectMethod(step, method)}
                    >
                      ${methodAction(method, this.#localizer)}
                    </button>
                  `;
                })}
              </div>`
            : html`<p class="completion-copy">
                ${this.#text("capturedWith", {
                  method: methodLabel(completion.acquisitionMethod, this.#localizer),
                })}
              </p>`
        }
        <p class="status" role="status" aria-live="polite">
          ${
            completion !== undefined
              ? completionStatus(
                  completion.acquisitionMethod,
                  this.#localizer,
                  completionAdapter === undefined
                    ? undefined
                    : captureMethodAdapterCopy(completionAdapter, this.#localizer.locale).label,
                )
              : uploadState?.status === "uploading"
                ? this.#text("preparingFileUpload")
                : uploadState?.status === "accepted"
                  ? this.#text("fileAccepted")
                  : cameraState?.status === "accepted"
                    ? this.#text("photoAccepted")
                    : selectedMethod === undefined
                      ? nothing
                      : this.#text("methodSelected", {
                          method: methodLabel(selectedMethod, this.#localizer),
                        })
          }
        </p>
      </section>
    `;
  }

  #renderFileUpload(
    step: CapturePlanStep,
    idSuffix: string,
    uploadState: StepUploadState | undefined,
    lockedByCamera: boolean,
  ) {
    const flow = this.#flowSnapshot;
    if (flow === undefined || !isActiveCaptureFlowSnapshot(flow)) return nothing;
    const policy = fileUploadPolicy(flow.session, step);
    const inputId = `idq-file-${idSuffix}`;
    const busy = uploadState?.status === "uploading";
    const reviewing = uploadState?.body !== undefined && uploadState.previewUrl !== undefined;
    return html`
      <div class="file-option">
        <input
          class="file-input"
          id=${inputId}
          name=${`evidence-${step.requirementKey}-${idSuffix}`}
          type="file"
          autocomplete="off"
          accept=${policy.allowedMediaTypes.join(",")}
          ?disabled=${busy || lockedByCamera}
          @change=${(event: Event) => void this.#fileSelected(step, event)}
        />
        ${
          reviewing
            ? html`
                <img
                  class="review-image"
                  src=${uploadState.previewUrl!}
                  alt=${this.#text("selectedFilePreview", {
                    artefact: friendlyArtefact(step.artefact, this.#localizer),
                  })}
                  width="640"
                  height="480"
                />
                <p class="camera-guidance">${this.#text("reviewSelectedFile")}</p>
                ${
                  uploadState.status === "error"
                    ? html`<p class="error" role="alert">${uploadState.message}</p>`
                    : nothing
                }
                <div class="camera-actions">
                  <button
                    class="primary"
                    type="button"
                    ?disabled=${busy}
                    @click=${() => void this.#uploadFile(step, uploadState.body!)}
                  >
                    ${uploadState.status === "error" ? this.#text("retryUpload") : this.#text("useFile")}
                  </button>
                  <label class="file-label" for=${inputId}
                    >${this.#text("chooseDifferentFile")}</label
                  >
                </div>
              `
            : html`
                <label class="file-label" for=${inputId}>
                  ${
                    busy
                      ? this.#text("uploadingFile")
                      : lockedByCamera
                        ? this.#text("finishCameraFirst")
                        : this.#text("chooseFile")
                  }
                </label>
                <p class="file-guidance">
                  ${this.#text("fileGuidance", {
                    mediaTypes: policy.allowedMediaTypes.map(mediaTypeLabel).join(" or "),
                  })}
                  ${
                    policy.hasExplicitMaximum
                      ? this.#text("maximumBytes", { maximum: formatBytes(policy.maximumBytes) })
                      : this.#text("serverSizeLimit")
                  }
                </p>
              `
        }
      </div>
    `;
  }

  #renderCamera(
    step: CapturePlanStep,
    idSuffix: string,
    state: StepCameraState | undefined,
    lockedByFile: boolean,
  ) {
    const videoId = `idq-camera-${idSuffix}`;
    const key = stepKey(step);
    const documentCaptureActive =
      isDocumentArtefact(step.artefact) &&
      this.#documentCapture.enabled &&
      this.#flowController !== undefined;
    const documentState = this.#documentStates.get(key);
    return html`
      <div class="camera-option">
        ${
          state === undefined || (state.status === "error" && state.previewUrl === undefined)
            ? html`<button
                type="button"
                ?disabled=${lockedByFile}
                @click=${() => void this.#startCamera(step, idSuffix)}
              >
                ${
                  lockedByFile
                    ? this.#text("fileUploadInProgress")
                    : state?.status === "error"
                      ? this.#text("retryCamera")
                      : this.#text("startCamera")
                }
              </button>`
            : nothing
        }
        ${
          state?.status === "requesting"
            ? html`<button type="button" disabled>${this.#text("requestingCamera")}</button>`
            : nothing
        }
        ${
          state?.status === "streaming"
            ? html`
                <div
                  class="document-preview-frame"
                  data-document-capture=${String(documentCaptureActive)}
                >
                  <video
                    class="camera-preview"
                    id=${videoId}
                    width="640"
                    height="480"
                    autoplay
                    muted
                    playsinline
                    aria-label=${this.#text("liveCameraPreviewLabel", {
                      artefact: friendlyArtefact(step.artefact, this.#localizer),
                    })}
                  ></video>
                  ${documentCaptureActive ? this.#renderDocumentGuide(state, documentState) : nothing}
                </div>
                <p class="camera-guidance">${this.#text("cameraGuidance")}</p>
                ${
                  documentCaptureActive && this.#documentCapture.autoCapture
                    ? html`<p class="document-auto-capture">
                        ${this.#text("documentAutoCapture")}
                      </p>`
                    : nothing
                }
                <div class="camera-actions">
                  <button
                    type="button"
                    ?disabled=${state.ready !== true || this.#capturingSteps.has(key)}
                    @click=${() =>
                      void this.#capturePhoto(
                        step,
                        videoId,
                        this.#documentObservations.get(key)?.detection,
                      )}
                  >
                    ${this.#text("capturePhoto")}
                  </button>
                  <button type="button" @click=${() => this.#cancelCamera(step)}>
                    ${this.#text("cancelCamera")}
                  </button>
                </div>
              `
            : nothing
        }
        ${
          (state?.status === "reviewing" ||
            state?.status === "uploading" ||
            (state?.status === "error" && state.previewUrl !== undefined)) &&
          state.previewUrl !== undefined &&
          state.width !== undefined &&
          state.height !== undefined
            ? html`
                <img
                  class="camera-preview"
                  src=${state.previewUrl}
                  alt=${this.#text("capturedPreviewLabel", {
                    artefact: friendlyArtefact(step.artefact, this.#localizer),
                  })}
                  width=${state.width}
                  height=${state.height}
                />
                <div class="camera-actions">
                  ${
                    state.body === undefined
                      ? nothing
                      : html`<button
                          class="primary"
                          type="button"
                          ?disabled=${state.status === "uploading"}
                          @click=${() => void this.#uploadCamera(step, state.body!)}
                        >
                          ${
                            state.status === "uploading"
                              ? this.#text("uploadingPhoto")
                              : this.#text("usePhoto")
                          }
                        </button>`
                  }
                  <button
                    type="button"
                    ?disabled=${state.status === "uploading"}
                    @click=${() => void this.#retakePhoto(step, idSuffix)}
                  >
                    ${this.#text("retakePhoto")}
                  </button>
                </div>
              `
            : nothing
        }
        <p class="status" role="status" aria-live="polite">
          ${
            state?.status === "requesting"
              ? this.#text("waitingForCameraPermission")
              : state?.status === "streaming"
                ? state.ready === true
                  ? documentCaptureActive
                    ? documentHintMessage(documentState, this.#localizer)
                    : this.#text("cameraReady")
                  : this.#text("startingCameraPreview")
                : state?.status === "reviewing"
                  ? this.#text("reviewCapturedPhoto")
                  : state?.status === "uploading"
                    ? this.#text("uploadingCapturedPhoto")
                    : nothing
          }
        </p>
        ${
          state?.status === "error"
            ? html`<p class="error" role="alert">${state.message}</p>`
            : nothing
        }
      </div>
    `;
  }

  #renderDocumentGuide(state: StepCameraState, documentState: StepDocumentState | undefined) {
    const width = state.width ?? 640;
    const height = state.height ?? 480;
    if (width <= 0 || height <= 0) return nothing;
    const guide = documentGuideRect(width, height);
    const quad = documentState?.quad;
    return html`
      <svg
        class="document-guide"
        viewBox="0 0 ${width} ${height}"
        preserveAspectRatio="xMidYMid meet"
        data-state=${documentState?.status ?? "searching"}
        aria-hidden="true"
        focusable="false"
      >
        <rect
          class="document-guide-target"
          x=${roundCoordinate(guide.x)}
          y=${roundCoordinate(guide.y)}
          width=${roundCoordinate(guide.width)}
          height=${roundCoordinate(guide.height)}
          rx=${roundCoordinate(Math.min(guide.width, guide.height) * 0.04)}
        ></rect>
        ${
          quad === undefined
            ? nothing
            : html`<polygon class="document-guide-quad" points=${quadPoints(quad)}></polygon>`
        }
      </svg>
    `;
  }

  #renderMethodAdapter(
    step: CapturePlanStep,
    idSuffix: string,
    adapter: CaptureMethodAdapter,
    state: StepAdapterState | undefined,
  ) {
    const copy = captureMethodAdapterCopy(adapter, this.#localizer.locale);
    const previewId = `idq-adapter-preview-${idSuffix}`;
    const running = state?.status === "running";
    const activeLiveness = adapter.presentation === "active_liveness";
    const prompt =
      state?.progress?.phase === "challenge" && state.progress.prompt !== undefined
        ? livenessPrompt(state.progress.prompt, this.#localizer)
        : undefined;
    return html`
      <div
        class="adapter-option"
        data-presentation=${adapter.presentation ?? "default"}
        aria-busy=${String(running)}
      >
        ${
          state?.previewStream === undefined
            ? nothing
            : html`<div class="adapter-preview-frame">
                <video
                  class="camera-preview"
                  id=${previewId}
                  width="640"
                  height="480"
                  autoplay
                  muted
                  playsinline
                  aria-label=${this.#text("adapterPreviewLabel", { method: copy.label })}
                ></video>
                ${
                  activeLiveness
                    ? html`
                        <span class="liveness-face-guide" aria-hidden="true"></span>
                        ${
                          prompt === undefined
                            ? nothing
                            : html`<span class="liveness-overlay-prompt" aria-hidden="true"
                                >${prompt}</span
                              >`
                        }
                      `
                    : nothing
                }
              </div>`
        }
        ${
          running
            ? html`
                <div class="adapter-progress" role="status" aria-live="polite">
                  ${
                    state.progress?.phase === "challenge" && prompt !== undefined
                      ? html`
                          <p class="eyebrow">
                            ${this.#text("challengeProgress", {
                              current: formatNumber(
                                state.progress.current!,
                                this.#localizer.locale,
                              ),
                              total: formatNumber(state.progress.total!, this.#localizer.locale),
                            })}
                          </p>
                          <p class="adapter-prompt">${prompt}</p>
                          <progress
                            aria-label=${this.#text("livenessProgressLabel")}
                            value=${state.progress.current!}
                            max=${state.progress.total!}
                          ></progress>
                        `
                      : nothing
                  }
                  ${
                    activeLiveness
                      ? html`<p class="liveness-auto-capture">
                          ${this.#text("livenessAutoCapture")}
                        </p>`
                      : nothing
                  }
                  <p>${adapterProgressMessage(state.progress, copy.label, this.#localizer)}</p>
                </div>
                <button type="button" @click=${() => this.#cancelMethodAdapter(step)}>
                  ${this.#text("cancelMethod", { method: copy.label })}
                </button>
              `
            : html`
                <button
                  class="primary"
                  type="button"
                  @click=${() => void this.#runMethodAdapter(step, adapter, previewId)}
                >
                  ${copy.action}
                </button>
              `
        }
        ${
          state?.status === "error"
            ? html`<p class="error" role="alert">${state.message}</p>`
            : nothing
        }
      </div>
    `;
  }

  #adapterFor(step: CapturePlanStep, method: string): CaptureMethodAdapter | undefined {
    const plan =
      this.#flowSnapshot !== undefined && isActiveCaptureFlowSnapshot(this.#flowSnapshot)
        ? this.#flowSnapshot.plan
        : this.plan;
    if (plan === undefined) return undefined;
    return findCaptureMethodAdapter(this.#methodAdapters, {
      verificationId: plan.verificationId,
      requirementKey: step.requirementKey,
      evidenceType: step.evidenceType,
      artefact: step.artefact,
      acquisitionMethod: method,
      ...(step.fallbackCondition === undefined
        ? {}
        : { fallbackCondition: step.fallbackCondition }),
    });
  }

  #assertMethodAdapters(plan: CapturePlan): void {
    for (const step of planSteps(plan)) {
      for (const method of step.methodOptions) {
        const context = {
          verificationId: plan.verificationId,
          requirementKey: step.requirementKey,
          evidenceType: step.evidenceType,
          artefact: step.artefact,
          acquisitionMethod: method,
          ...(step.fallbackCondition === undefined
            ? {}
            : { fallbackCondition: step.fallbackCondition }),
        };
        const adapter = findCaptureMethodAdapter(this.#methodAdapters, context);
        if (adapter !== undefined) captureMethodAdapterCopy(adapter, this.#localizer.locale);
        if (
          method !== FILE_UPLOAD_METHOD &&
          method !== LIVE_CAMERA_METHOD &&
          adapter === undefined
        ) {
          requireCaptureMethodAdapter(this.#methodAdapters, context);
        }
      }
    }
  }

  async #runMethodAdapter(
    step: CapturePlanStep,
    adapter: CaptureMethodAdapter,
    previewId: string,
  ): Promise<void> {
    const flowController = this.#flowController;
    const parentSignal = this.#flowAbortController?.signal;
    const plan =
      this.#flowSnapshot !== undefined && isActiveCaptureFlowSnapshot(this.#flowSnapshot)
        ? this.#flowSnapshot.plan
        : undefined;
    const key = stepKey(step);
    if (
      flowController === undefined ||
      parentSignal === undefined ||
      plan === undefined ||
      parentSignal.aborted ||
      this.#completedSteps.has(key) ||
      this.#adapterStates.get(key)?.status === "running"
    ) {
      return;
    }
    this.#clearUploadState(key);
    this.#clearCameraState(key);
    const controller = new AbortController();
    const parentAborted = () => controller.abort(parentSignal.reason);
    parentSignal.addEventListener("abort", parentAborted, { once: true });
    this.#adapterStates.set(key, { status: "running", controller });
    this.#selectMethod(step, adapter.method);
    this.#reportStep(step, adapter.method, "started");
    this.requestUpdate();
    const context: CaptureMethodAdapterContext = {
      verificationId: plan.verificationId,
      requirementKey: step.requirementKey,
      evidenceType: step.evidenceType,
      artefact: step.artefact,
      acquisitionMethod: adapter.method,
      ...(step.fallbackCondition === undefined
        ? {}
        : { fallbackCondition: step.fallbackCondition }),
      locale: this.#localizer.locale,
      signal: controller.signal,
    };
    try {
      await adapter.acquire(context, {
        update: (progress) => {
          const current = this.#adapterStates.get(key);
          if (current?.controller !== controller || controller.signal.aborted) return;
          this.#adapterStates.set(key, {
            ...current,
            progress: normalizeCaptureMethodProgress(progress),
          });
          this.requestUpdate();
        },
        setPreview: (stream) => this.#setAdapterPreview(key, controller, previewId, stream),
      });
      if (controller.signal.aborted) return;
      const snapshot = await flowController.refresh(controller.signal);
      if (controller.signal.aborted) return;
      this.#flowSnapshot = snapshot;
      this.#restoreRecoveredProgress(snapshot);
      if (!this.#completedSteps.has(key)) {
        throw new CaptureMethodAdapterError(
          "CAPTURE_METHOD_ADAPTER_UNCONFIRMED",
          "Core did not confirm this acquisition. Check the integration and retry.",
        );
      }
      this.#releaseAdapterPreview(key, controller, previewId);
      this.#adapterStates.delete(key);
    } catch (error) {
      if (parentSignal.aborted) return;
      if (controller.signal.aborted) {
        this.#adapterStates.delete(key);
        this.requestUpdate();
        return;
      }
      const unconfirmed =
        error instanceof CaptureMethodAdapterError &&
        error.code === "CAPTURE_METHOD_ADAPTER_UNCONFIRMED";
      this.#releaseAdapterPreview(key, controller, previewId);
      this.#adapterStates.set(key, {
        status: "error",
        controller,
        message: unconfirmed ? this.#text("adapterUnconfirmed") : this.#text("adapterFailed"),
      });
      this.#reportStep(
        step,
        adapter.method,
        "failed",
        unconfirmed ? "adapter_completion_unconfirmed" : "adapter_failed",
      );
      this.#dispatchFlowError();
    } finally {
      parentSignal.removeEventListener("abort", parentAborted);
      this.requestUpdate();
    }
  }

  async #setAdapterPreview(
    key: string,
    controller: AbortController,
    previewId: string,
    stream: MediaStream | undefined,
  ): Promise<void> {
    const current = this.#adapterStates.get(key);
    if (current?.controller !== controller || controller.signal.aborted) return;
    if (stream !== undefined && !(stream instanceof MediaStream)) {
      throw new CaptureMethodAdapterError(
        "CAPTURE_METHOD_ADAPTER_INVALID",
        "The acquisition adapter preview must be a MediaStream.",
      );
    }
    if (stream === undefined && current.previewStream !== undefined) {
      const currentVideo = this.renderRoot.querySelector<HTMLVideoElement>(`#${previewId}`);
      if (currentVideo !== null) currentVideo.srcObject = null;
    }
    this.#adapterStates.set(
      key,
      stream === undefined
        ? {
            status: current.status,
            controller,
            ...(current.progress === undefined ? {} : { progress: current.progress }),
            ...(current.message === undefined ? {} : { message: current.message }),
          }
        : { ...current, previewStream: stream },
    );
    this.requestUpdate();
    await this.updateComplete;
    const video = this.renderRoot.querySelector<HTMLVideoElement>(`#${previewId}`);
    if (stream === undefined) {
      if (video !== null) video.srcObject = null;
      return;
    }
    if (video === null || controller.signal.aborted) return;
    video.srcObject = stream;
    await video.play();
  }

  #releaseAdapterPreview(key: string, controller: AbortController, previewId: string): void {
    const current = this.#adapterStates.get(key);
    if (current?.controller !== controller || current.previewStream === undefined) return;
    const video = this.renderRoot.querySelector<HTMLVideoElement>(`#${previewId}`);
    if (video !== null) video.srcObject = null;
    stopCamera(current.previewStream);
  }

  #cancelMethodAdapter(step: CapturePlanStep): void {
    const key = stepKey(step);
    const state = this.#adapterStates.get(key);
    if (state === undefined) return;
    state.controller.abort(new DOMException("The acquisition method was cancelled.", "AbortError"));
    if (state.previewStream !== undefined) stopCamera(state.previewStream);
    this.#adapterStates.delete(key);
    const method = this.#selectedMethods.get(key) ?? step.methodOptions[0]!;
    this.#reportStep(step, method, "cancelled", "subject_cancelled");
    this.requestUpdate();
  }

  #clearAdapterState(key: string): void {
    const state = this.#adapterStates.get(key);
    if (state === undefined) return;
    state.controller.abort(new DOMException("The acquisition method was stopped.", "AbortError"));
    if (state.previewStream !== undefined) stopCamera(state.previewStream);
    this.#adapterStates.delete(key);
  }

  #clearAdapterStates(): void {
    for (const key of [...this.#adapterStates.keys()]) this.#clearAdapterState(key);
  }

  #selectMethod(step: CapturePlanStep, method: string): void {
    const key = stepKey(step);
    if (this.#completedSteps.has(key)) return;
    this.#selectedMethods.set(key, method);
    this.requestUpdate();
    const detail: CaptureMethodSelectDetail = {
      requirementKey: step.requirementKey,
      evidenceType: step.evidenceType,
      artefact: step.artefact,
      method,
      ...(step.fallbackCondition === undefined
        ? {}
        : { fallbackCondition: step.fallbackCondition }),
    };
    this.dispatchEvent(
      new CustomEvent<CaptureMethodSelectDetail>("idenqa-method-select", {
        bubbles: true,
        composed: true,
        detail,
      }),
    );
  }

  async #fileSelected(step: CapturePlanStep, event: Event): Promise<void> {
    const input = event.currentTarget as HTMLInputElement;
    const body = input.files?.item(0);
    input.value = "";
    const key = stepKey(step);
    if (body === null || body === undefined || this.#completedSteps.has(key)) return;
    if (
      isCameraActive(this.#cameraStates.get(key)) ||
      this.#adapterStates.get(key)?.status === "running"
    ) {
      return;
    }
    this.#clearCameraState(key);
    this.#clearUploadState(key);
    this.#selectMethod(step, FILE_UPLOAD_METHOD);
    this.#uploadStates.set(key, {
      status: "reviewing",
      body,
      previewUrl: URL.createObjectURL(body),
    });
    this.requestUpdate();
  }

  async #uploadFile(step: CapturePlanStep, body: Blob): Promise<void> {
    const controller = this.#flowController;
    const signal = this.#flowAbortController?.signal;
    if (
      controller === undefined ||
      signal === undefined ||
      signal.aborted ||
      this.#completedSteps.has(stepKey(step))
    ) {
      return;
    }
    const key = stepKey(step);
    const current = this.#uploadStates.get(key);
    this.#uploadStates.set(key, {
      status: "uploading",
      body,
      ...(current?.previewUrl === undefined ? {} : { previewUrl: current.previewUrl }),
    });
    this.#reportStep(step, FILE_UPLOAD_METHOD, "started");
    this.requestUpdate();
    try {
      const upload = await controller.uploadFile(step, body, signal);
      if (signal.aborted) return;
      this.#clearCameraState(key);
      if (current?.previewUrl !== undefined) URL.revokeObjectURL(current.previewUrl);
      this.#uploadStates.set(key, { status: "accepted" });
      this.#completeStep(step, FILE_UPLOAD_METHOD, upload);
    } catch (error) {
      if (signal.aborted) return;
      const message = this.#text("fileRejected");
      this.#uploadStates.set(key, {
        status: "error",
        body,
        message,
        ...(current?.previewUrl === undefined ? {} : { previewUrl: current.previewUrl }),
      });
      this.#reportStep(step, FILE_UPLOAD_METHOD, "failed", "file_upload_failed");
      this.#dispatchFlowError();
    }
    this.requestUpdate();
  }

  async #startCamera(step: CapturePlanStep, idSuffix: string): Promise<void> {
    const signal = this.#flowAbortController?.signal;
    if (this.#flowController === undefined || signal === undefined || signal.aborted) return;
    const key = stepKey(step);
    if (
      this.#completedSteps.has(key) ||
      this.#uploadStates.get(key)?.status === "uploading" ||
      this.#adapterStates.get(key)?.status === "running"
    )
      return;
    this.#clearCameraState(key);
    this.#uploadStates.delete(key);
    this.#selectMethod(step, LIVE_CAMERA_METHOD);
    this.#cameraStates.set(key, { status: "requesting" });
    this.#reportStep(step, LIVE_CAMERA_METHOD, "started");
    this.requestUpdate();
    try {
      const stream = await startCamera(cameraFacingMode(step.artefact), signal);
      if (signal.aborted) {
        stopCamera(stream);
        return;
      }
      this.#cameraStates.set(key, { status: "streaming", stream, ready: false });
      this.requestUpdate();
      await this.updateComplete;
      const videoId = `idq-camera-${idSuffix}`;
      const video = this.renderRoot.querySelector<HTMLVideoElement>(`#${videoId}`);
      if (video === null) {
        stopCamera(stream);
        return;
      }
      video.srcObject = stream;
      video.addEventListener(
        "loadedmetadata",
        () => this.#markCameraReady(step, key, stream, video),
        {
          once: true,
        },
      );
      await video.play();
      this.#markCameraReady(step, key, stream, video);
    } catch (error) {
      if (signal.aborted) return;
      this.#handleCameraFailure(step, error);
    }
  }

  async #capturePhoto(
    step: CapturePlanStep,
    videoId: string,
    detection?: DocumentDetection,
  ): Promise<void> {
    const key = stepKey(step);
    const state = this.#cameraStates.get(key);
    const video = this.renderRoot.querySelector<HTMLVideoElement>(`#${videoId}`);
    if (state?.status !== "streaming" || state.ready !== true || video === null) return;
    if (this.#capturingSteps.has(key)) return;
    const flow = this.#flowSnapshot;
    if (flow === undefined || !isActiveCaptureFlowSnapshot(flow)) return;
    this.#capturingSteps.add(key);
    this.#stopDocumentObservation(key);
    try {
      const policy = fileUploadPolicy(flow.session, step);
      const mediaType = policy.allowedMediaTypes.includes("image/jpeg")
        ? "image/jpeg"
        : policy.allowedMediaTypes[0]!;
      let frame: CameraFrame | undefined;
      if (detection !== undefined && isDocumentArtefact(step.artefact)) {
        frame = await captureCorrectedDocumentFrame(
          video,
          mediaType,
          detection,
          this.#documentCapture,
        );
      }
      const captured = frame ?? (await captureCameraFrame(video, mediaType));
      stopCamera(state.stream);
      video.srcObject = null;
      this.#cameraStates.set(key, {
        status: "reviewing",
        body: captured.body,
        previewUrl: URL.createObjectURL(captured.body),
        width: captured.width,
        height: captured.height,
      });
      this.requestUpdate();
    } catch (error) {
      this.#handleCameraFailure(step, error);
    } finally {
      this.#capturingSteps.delete(key);
    }
  }

  async #uploadCamera(step: CapturePlanStep, body: Blob): Promise<void> {
    const controller = this.#flowController;
    const signal = this.#flowAbortController?.signal;
    const key = stepKey(step);
    const current = this.#cameraStates.get(key);
    if (
      controller === undefined ||
      signal === undefined ||
      signal.aborted ||
      current?.previewUrl === undefined ||
      current.width === undefined ||
      current.height === undefined
    ) {
      return;
    }
    this.#cameraStates.set(key, { ...current, status: "uploading", body });
    this.requestUpdate();
    try {
      const upload = await controller.uploadCamera(step, body, signal);
      if (signal.aborted) return;
      URL.revokeObjectURL(current.previewUrl);
      this.#uploadStates.delete(key);
      this.#cameraStates.set(key, { status: "accepted" });
      this.#completeStep(step, LIVE_CAMERA_METHOD, upload);
    } catch (error) {
      if (signal.aborted) return;
      const message = this.#text("photoRejected");
      this.#cameraStates.set(key, { ...current, status: "error", body, message });
      this.#reportStep(step, LIVE_CAMERA_METHOD, "failed", "camera_upload_failed");
      this.#dispatchFlowError();
    }
    this.requestUpdate();
  }

  async #retakePhoto(step: CapturePlanStep, idSuffix: string): Promise<void> {
    this.#clearCameraState(stepKey(step));
    await this.#startCamera(step, idSuffix);
  }

  #cancelCamera(step: CapturePlanStep): void {
    const key = stepKey(step);
    this.#clearCameraState(key);
    this.#selectedMethods.delete(key);
    this.#reportStep(step, LIVE_CAMERA_METHOD, "cancelled", "subject_cancelled");
    this.requestUpdate();
  }

  #handleCameraFailure(step: CapturePlanStep, error: unknown): void {
    const key = stepKey(step);
    this.#clearCameraState(key);
    const controller = this.#flowController;
    this.#reportStep(step, LIVE_CAMERA_METHOD, "failed", "camera_capture_failed");
    if (controller !== undefined) {
      try {
        this.#flowSnapshot = controller.captureFailed(step);
        this.#selectedMethods.delete(key);
        if (this.#journeyPhase === "capture") {
          const fallbackStep = planSteps(this.#flowSnapshot.plan)[this.#activeStepIndex];
          if (fallbackStep !== undefined && fallbackStep.methodOptions.length === 1) {
            this.#selectedMethods.set(stepKey(fallbackStep), fallbackStep.methodOptions[0]!);
            this.#stepStage = "preparation";
          } else {
            this.#stepStage = "method";
          }
        }
        this.requestUpdate();
        return;
      } catch {
        // No usable capture_failed fallback exists; keep the camera retry local.
      }
    }
    this.#cameraStates.set(key, {
      status: "error",
      message: cameraErrorMessage(error, this.#localizer),
    });
    this.#dispatchFlowError();
    this.requestUpdate();
  }

  #clearCameraState(key: string): void {
    this.#stopDocumentObservation(key);
    this.#capturingSteps.delete(key);
    const state = this.#cameraStates.get(key);
    stopCamera(state?.stream);
    if (state?.previewUrl !== undefined) URL.revokeObjectURL(state.previewUrl);
    this.#cameraStates.delete(key);
  }

  #clearUploadState(key: string): void {
    const state = this.#uploadStates.get(key);
    if (state?.previewUrl !== undefined) URL.revokeObjectURL(state.previewUrl);
    this.#uploadStates.delete(key);
  }

  #clearUploadStates(): void {
    for (const key of [...this.#uploadStates.keys()]) this.#clearUploadState(key);
  }

  #markCameraReady(
    step: CapturePlanStep,
    key: string,
    stream: MediaStream,
    video: HTMLVideoElement,
  ): void {
    const state = this.#cameraStates.get(key);
    if (
      state?.status !== "streaming" ||
      state.stream !== stream ||
      video.videoWidth <= 0 ||
      video.videoHeight <= 0
    ) {
      return;
    }
    this.#cameraStates.set(key, {
      ...state,
      ready: true,
      width: video.videoWidth,
      height: video.videoHeight,
    });
    if (
      isDocumentArtefact(step.artefact) &&
      this.#documentCapture.enabled &&
      !this.#documentObservations.has(key)
    ) {
      this.#startDocumentObservation(step, key, video);
    }
    this.requestUpdate();
  }

  #clearCameraStates(): void {
    for (const key of [...this.#documentObservations.keys()]) this.#stopDocumentObservation(key);
    for (const key of [...this.#cameraStates.keys()]) this.#clearCameraState(key);
    this.#documentStates.clear();
    this.#capturingSteps.clear();
  }

  #startDocumentObservation(step: CapturePlanStep, key: string, video: HTMLVideoElement): void {
    this.#stopDocumentObservation(key);
    const observer = createDocumentFrameObserver(this.#documentCapture);
    const gate = createDocumentCaptureGate(this.#documentCapture.gate);
    const observation: StepDocumentObservation = {
      interval: undefined,
      autoCaptureTimer: undefined,
      observer,
      gate,
      detection: undefined,
    };
    observation.interval = globalThis.setInterval(() => {
      this.#observeDocument(step, key, video, observation);
    }, this.#documentCapture.observationIntervalMs);
    this.#documentObservations.set(key, observation);
    this.#setDocumentState(key, { status: "searching", progress: 0 });
  }

  #observeDocument(
    step: CapturePlanStep,
    key: string,
    video: HTMLVideoElement,
    observation: StepDocumentObservation,
  ): void {
    if (this.#documentObservations.get(key) !== observation) return;
    const camera = this.#cameraStates.get(key);
    if (camera?.status !== "streaming" || camera.ready !== true) return;
    if (this.#capturingSteps.has(key) || this.#completedSteps.has(key)) return;
    const observed = observation.observer.observe(video);
    if (observed === undefined) return;
    const result = observation.gate.observe(observed);
    observation.detection = observed.detection;
    this.#setDocumentState(key, {
      status: result.status,
      progress: result.requiredFrames === 0 ? 0 : result.stableFrames / result.requiredFrames,
      ...(result.reason === undefined ? {} : { reason: result.reason }),
      ...(observed.detection === undefined
        ? {}
        : {
            quad: observed.detection.quad,
            frameWidth: observed.detection.frameWidth,
            frameHeight: observed.detection.frameHeight,
          }),
    });
    if (
      result.status === "ready" &&
      this.#documentCapture.autoCapture &&
      observed.detection !== undefined &&
      observation.autoCaptureTimer === undefined
    ) {
      const detection = observed.detection;
      observation.autoCaptureTimer = globalThis.setTimeout(() => {
        observation.autoCaptureTimer = undefined;
        if (this.#documentObservations.get(key) !== observation) return;
        void this.#capturePhoto(step, video.id, detection);
      }, this.#documentCapture.autoCaptureDelayMs);
    }
  }

  #stopDocumentObservation(key: string): void {
    const observation = this.#documentObservations.get(key);
    if (observation === undefined) return;
    if (observation.interval !== undefined) globalThis.clearInterval(observation.interval);
    if (observation.autoCaptureTimer !== undefined) {
      globalThis.clearTimeout(observation.autoCaptureTimer);
    }
    observation.observer.dispose();
    this.#documentObservations.delete(key);
    this.#documentStates.delete(key);
  }

  #setDocumentState(key: string, next: StepDocumentState): void {
    const current = this.#documentStates.get(key);
    if (current !== undefined && documentStateSignature(current) === documentStateSignature(next)) {
      return;
    }
    this.#documentStates.set(key, next);
    this.requestUpdate();
  }

  #completeStep(step: CapturePlanStep, acquisitionMethod: string, upload: EvidenceUpload): void {
    const key = stepKey(step);
    if (this.#completedSteps.has(key)) return;
    this.#completedSteps.set(key, {
      acquisitionMethod,
      uploadId: upload.id,
      evidenceId: upload.evidenceId,
    });
    const detail: CaptureEvidenceAcceptedDetail = {
      uploadId: upload.id,
      evidenceId: upload.evidenceId,
      requirementKey: step.requirementKey,
      artefact: step.artefact,
      acquisitionMethod,
    };
    this.dispatchEvent(
      new CustomEvent<CaptureEvidenceAcceptedDetail>("idenqa-evidence-accepted", {
        bubbles: true,
        composed: true,
        detail,
      }),
    );
    const plan =
      this.#flowSnapshot !== undefined && isActiveCaptureFlowSnapshot(this.#flowSnapshot)
        ? this.#flowSnapshot.plan
        : this.plan;
    if (plan === undefined) return;
    const progress = captureProgress(plan, this.#completedSteps);
    if (this.#flowSnapshot !== undefined && this.#journeyPhase === "capture") {
      this.#journeyPhase =
        progress.completedSteps === progress.totalSteps ? "processing" : "confirmation";
      this.requestUpdate();
    }
    const progressDetail: CaptureProgressDetail = {
      verificationId: plan.verificationId,
      ...progress,
    };
    this.dispatchEvent(
      new CustomEvent<CaptureProgressDetail>("idenqa-capture-progress", {
        bubbles: true,
        composed: true,
        detail: progressDetail,
      }),
    );
    if (progress.completedSteps !== progress.totalSteps || this.#captureCompleteDispatched) return;
    this.#captureCompleteDispatched = true;
    this.#pollAuthoritativeOutcome();
    const completeDetail: CaptureCompleteDetail = { ...progressDetail, captureComplete: true };
    this.dispatchEvent(
      new CustomEvent<CaptureCompleteDetail>("idenqa-capture-complete", {
        bubbles: true,
        composed: true,
        detail: completeDetail,
      }),
    );
  }

  #restoreRecoveredProgress(snapshot: CaptureFlowSnapshot): void {
    if (!isActiveCaptureFlowSnapshot(snapshot)) return;
    const steps = snapshot.plan.requirements.flatMap((requirement) => requirement.steps);
    for (const completion of snapshot.progress.completions) {
      const matches = steps.filter(
        (step) =>
          step.requirementKey === completion.requirementKey &&
          step.evidenceType === completion.evidenceType &&
          step.artefact === completion.artefact &&
          step.methodOptions.includes(completion.acquisitionMethod) &&
          step.fallbackCondition === completion.fallbackCondition,
      );
      if (matches.length !== 1) continue;
      this.#completedSteps.set(stepKey(matches[0]!), {
        acquisitionMethod: completion.acquisitionMethod,
        uploadId: completion.uploadId,
        evidenceId: completion.evidenceId,
      });
    }
    const progress = captureProgress(snapshot.plan, this.#completedSteps);
    const activeStep = planSteps(snapshot.plan)[this.#activeStepIndex];
    if (
      this.#journeyPhase === "capture" &&
      activeStep !== undefined &&
      this.#completedSteps.has(stepKey(activeStep))
    ) {
      this.#journeyPhase =
        progress.completedSteps === progress.totalSteps ? "processing" : "confirmation";
    }
    if (this.#completedSteps.size === 0) return;
    const detail: CaptureProgressDetail = {
      verificationId: snapshot.plan.verificationId,
      ...progress,
    };
    this.dispatchEvent(
      new CustomEvent<CaptureProgressDetail>("idenqa-capture-progress", {
        bubbles: true,
        composed: true,
        detail,
      }),
    );
    if (progress.completedSteps !== progress.totalSteps) return;
    this.#captureCompleteDispatched = true;
    this.dispatchEvent(
      new CustomEvent<CaptureCompleteDetail>("idenqa-capture-complete", {
        bubbles: true,
        composed: true,
        detail: { ...detail, captureComplete: true },
      }),
    );
  }

  async #respond(action: SubjectResponseAction): Promise<void> {
    const controller = this.#flowController;
    const signal = this.#flowAbortController?.signal;
    if (controller === undefined || this.#flowState === "responding") return;
    this.#flowState = "responding";
    this.#responseError = false;
    this.requestUpdate();
    try {
      const snapshot = await controller.respond(action, signal);
      if (signal?.aborted === true) return;
      this.#flowSnapshot = snapshot;
      this.#flowState = "ready";
      const response = snapshot.authoritySnapshot.latestResponse;
      if (response !== undefined) {
        const detail: CaptureSubjectResponseDetail = {
          action: response.action,
          responseId: response.id,
          authorityId: response.authorityId,
          noticeId: response.noticeId,
        };
        this.dispatchEvent(
          new CustomEvent<CaptureSubjectResponseDetail>("idenqa-subject-response", {
            bubbles: true,
            composed: true,
            detail,
          }),
        );
      }
      if (snapshot.status === "refused" || snapshot.status === "authority_blocked") {
        this.#dropFlowController();
      } else if (snapshot.status === "capture_ready") {
        this.#resumeJourney(snapshot.plan, true);
      }
    } catch {
      if (signal?.aborted === true) return;
      this.#flowState = "ready";
      this.#responseError = true;
      this.#dispatchFlowError();
    }
    this.requestUpdate();
  }

  #dispatchFlowError(): void {
    const detail: CaptureFlowErrorDetail = { code: "CAPTURE_FLOW_REQUEST_FAILED" };
    this.dispatchEvent(
      new CustomEvent<CaptureFlowErrorDetail>("idenqa-flow-error", {
        bubbles: true,
        composed: true,
        detail,
      }),
    );
  }

  #dispatchRealtimeEvent(event: CaptureRealtimeEvent): void {
    this.dispatchEvent(
      new CustomEvent<CaptureRealtimeDetail>("idenqa-realtime-event", {
        bubbles: true,
        composed: true,
        detail: { event },
      }),
    );
  }

  #reportStep(
    step: CapturePlanStep,
    acquisitionMethod: string,
    state: "started" | "failed" | "cancelled",
    code?: string,
  ): void {
    void this.#flowController
      ?.reportStep({
        state,
        requirementKey: step.requirementKey,
        artefact: step.artefact,
        acquisitionMethod,
        ...(code === undefined ? {} : { code }),
      })
      .catch(() => this.#dispatchFlowError());
  }

  #dropFlowController(): void {
    this.#clearCameraStates();
    this.#clearAdapterStates();
    this.#flowAbortController?.abort();
    this.#flowAbortController = undefined;
    this.#flowController = undefined;
    this.#outcomePolling = false;
  }

  #pollAuthoritativeOutcome(): void {
    const controller = this.#flowController;
    const abortController = this.#flowAbortController;
    if (controller === undefined || abortController === undefined || this.#outcomePolling) return;
    this.#outcomePolling = true;
    void controller
      .pollOutcome((recovered) => {
        if (abortController.signal.aborted || this.#flowController !== controller) return;
        this.#flowSnapshot = recovered;
        if (isActiveCaptureFlowSnapshot(recovered)) {
          this.#restoreRecoveredProgress(recovered);
        } else if (recovered.status !== "processing" && recovered.status !== "action_required") {
          this.#dropFlowController();
        }
        this.requestUpdate();
      }, abortController.signal)
      .catch(() => {
        if (!abortController.signal.aborted && this.#flowController === controller) {
          this.#dispatchFlowError();
        }
      })
      .finally(() => {
        if (this.#flowController === controller) this.#outcomePolling = false;
      });
  }

  #text(key: CaptureMessageKey, values?: CaptureMessageValues): string {
    return this.#localizer.text(key, values);
  }
}

export function defineIdenqaCapture(
  tagName: string = IDENQA_CAPTURE_TAG_NAME,
): typeof IdenqaCaptureElement {
  if (typeof customElements === "undefined") {
    throw new Error("Custom elements are not available in this environment.");
  }
  const existing = customElements.get(tagName);
  if (existing === undefined) {
    customElements.define(tagName, IdenqaCaptureElement);
  } else if (existing !== IdenqaCaptureElement) {
    throw new Error(`The custom-element name ${tagName} is already registered.`);
  }
  return IdenqaCaptureElement;
}

function stepKey(step: CapturePlanStep): string {
  return JSON.stringify([
    step.requirementKey,
    step.artefact,
    step.methodOptions,
    step.fallbackCondition,
  ]);
}

function documentStateSignature(state: StepDocumentState): string {
  const quad = state.quad;
  const points =
    quad === undefined
      ? ""
      : [quad.topLeft, quad.topRight, quad.bottomRight, quad.bottomLeft]
          .map((point) => `${point.x.toFixed(1)},${point.y.toFixed(1)}`)
          .join(";");
  return `${state.status}|${state.reason ?? ""}|${state.progress.toFixed(2)}|${points}|${state.frameWidth ?? 0}x${state.frameHeight ?? 0}`;
}

function documentHintMessage(
  state: StepDocumentState | undefined,
  localizer: CaptureLocalizer,
): string {
  if (state === undefined || state.status === "searching") {
    return localizer.text("documentSearching");
  }
  if (state.status === "ready") return localizer.text("documentReady");
  if (state.status === "steadying") return localizer.text("documentHoldSteady");
  return state.reason === "low_coverage"
    ? localizer.text("documentMoveCloser")
    : localizer.text("documentAligning");
}

function documentGuideRect(
  width: number,
  height: number,
): { readonly x: number; readonly y: number; readonly width: number; readonly height: number } {
  const maximumWidth = width * 0.86;
  const maximumHeight = height * 0.86;
  let guideWidth = maximumWidth;
  let guideHeight = guideWidth / DOCUMENT_GUIDE_ASPECT_RATIO;
  if (guideHeight > maximumHeight) {
    guideHeight = maximumHeight;
    guideWidth = guideHeight * DOCUMENT_GUIDE_ASPECT_RATIO;
  }
  return {
    x: (width - guideWidth) / 2,
    y: (height - guideHeight) / 2,
    width: guideWidth,
    height: guideHeight,
  };
}

function quadPoints(quad: DocumentQuad): string {
  return [quad.topLeft, quad.topRight, quad.bottomRight, quad.bottomLeft]
    .map((point) => `${roundCoordinate(point.x)},${roundCoordinate(point.y)}`)
    .join(" ");
}

function roundCoordinate(value: number): number {
  return Math.round(value * 10) / 10;
}

function captureProgress(
  plan: CapturePlan,
  completedSteps: ReadonlyMap<string, StepCompletion>,
): Pick<CaptureProgressDetail, "completedSteps" | "totalSteps"> {
  const steps = plan.requirements.flatMap((requirement) => requirement.steps);
  return {
    completedSteps: steps.filter((step) => completedSteps.has(stepKey(step))).length,
    totalSteps: steps.length,
  };
}

function isCameraActive(state: StepCameraState | undefined): boolean {
  return (
    state?.status === "requesting" ||
    state?.status === "streaming" ||
    state?.status === "reviewing" ||
    state?.status === "uploading"
  );
}

function isCameraBusy(state: StepCameraState | undefined): boolean {
  return (
    state?.status === "requesting" || state?.status === "streaming" || state?.status === "uploading"
  );
}

function formatNumber(value: number, locale: string): string {
  return new Intl.NumberFormat(locale).format(value);
}

function completionStatus(
  method: string,
  localizer: CaptureLocalizer,
  adapterLabel?: string,
): string {
  if (adapterLabel !== undefined) {
    return localizer.text("adapterStepComplete", { method: adapterLabel });
  }
  return method === LIVE_CAMERA_METHOD
    ? localizer.text("cameraStepComplete")
    : localizer.text("fileStepComplete");
}

function adapterProgressMessage(
  progress: CaptureMethodAdapterProgress | undefined,
  method: string,
  localizer: CaptureLocalizer,
): string {
  if (progress === undefined || progress.phase === "preparing") {
    return localizer.text("adapterPreparing", { method });
  }
  if (progress.phase === "requesting_permission") {
    return localizer.text("adapterRequestingPermission", { method });
  }
  if (progress.phase === "ready") return localizer.text("adapterReady", { method });
  if (progress.phase === "submitting") {
    return localizer.text("adapterSubmitting", { method });
  }
  return localizer.text("adapterRunning", { method });
}

function livenessPrompt(
  prompt: NonNullable<CaptureMethodAdapterProgress["prompt"]>,
  localizer: CaptureLocalizer,
): string {
  switch (prompt) {
    case "neutral":
      return localizer.text("livenessNeutral");
    case "turn_left":
      return localizer.text("livenessTurnLeft");
    case "turn_right":
      return localizer.text("livenessTurnRight");
    case "look_up":
      return localizer.text("livenessLookUp");
    case "look_down":
      return localizer.text("livenessLookDown");
    case "blink":
      return localizer.text("livenessBlink");
  }
}

function fallbackMessage(
  condition: CaptureRuntimeFallbackReason,
  localizer: CaptureLocalizer,
): string {
  if (condition === "capability_unavailable") {
    return localizer.text("capabilityFallback");
  }
  if (condition === "capture_failed") {
    return localizer.text("captureFailureFallback");
  }
  return localizer.text("methodFallback");
}

function cameraErrorMessage(error: unknown, localizer: CaptureLocalizer): string {
  if (error instanceof CaptureCameraError && error.code === "CAPTURE_CAMERA_PERMISSION_DENIED") {
    return localizer.text("cameraPermissionDenied");
  }
  if (error instanceof CaptureCameraError && error.code === "CAPTURE_CAMERA_FRAME_FAILED") {
    return localizer.text("cameraFrameFailed", { message: error.message });
  }
  return localizer.text("cameraUnavailable");
}

function methodDescription(method: string, localizer: CaptureLocalizer): string {
  if (method === FILE_UPLOAD_METHOD) return localizer.text("fileMethodDescription");
  if (method === LIVE_CAMERA_METHOD) return localizer.text("cameraMethodDescription");
  return localizer.text("otherMethodDescription");
}

function captureInstruction(method: string, localizer: CaptureLocalizer): string {
  if (method === FILE_UPLOAD_METHOD) return localizer.text("fileInstruction");
  if (method === LIVE_CAMERA_METHOD) return localizer.text("cameraInstruction");
  return localizer.text("otherInstruction");
}

function friendlyArtefact(artefact: string, localizer: CaptureLocalizer): string {
  if (artefact === "idenqa.artefact.selfie_image") return localizer.text("selfie");
  if (artefact === "idenqa.artefact.document_front") return localizer.text("documentFront");
  if (artefact === "idenqa.artefact.document_back") return localizer.text("documentBack");
  return labelForIdentifier(artefact);
}

function methodAction(method: string, localizer: CaptureLocalizer): string {
  if (method === "idenqa.method.file_upload") return localizer.text("uploadFileAction");
  if (method === "idenqa.method.live_camera") return localizer.text("useCameraAction");
  return localizer.text("useMethodAction", { method: methodLabel(method, localizer) });
}

function methodLabel(method: string, localizer: CaptureLocalizer): string {
  if (method === "idenqa.method.file_upload") return localizer.text("fileUploadMethod");
  if (method === "idenqa.method.live_camera") return localizer.text("cameraMethod");
  return labelForIdentifier(method);
}

function mediaTypeLabel(mediaType: string): string {
  return mediaType === "image/jpeg" ? "JPEG" : mediaType === "image/png" ? "PNG" : mediaType;
}

function labelForIdentifier(identifier: string): string {
  const segment = identifier.split(".").at(-1) ?? identifier;
  return segment
    .split("_")
    .filter((word) => word.length > 0)
    .map((word) => word[0]?.toUpperCase() + word.slice(1))
    .join(" ");
}

function browserLocale(): string {
  return globalThis.navigator?.languages?.[0] ?? globalThis.navigator?.language ?? "en";
}

function mergeCaptureMessageCatalogues(
  base: CaptureMessageCatalogue | undefined,
  override: CaptureMessageCatalogue,
): CaptureMessageCatalogue {
  if (base === undefined) return override;
  const merged: Record<string, Partial<Record<CaptureMessageKey, string>>> = {};
  for (const locale of new Set([...Object.keys(base), ...Object.keys(override)])) {
    merged[locale] = { ...base[locale], ...override[locale] };
  }
  return merged;
}

function planSteps(plan: CapturePlan): readonly CapturePlanStep[] {
  return plan.requirements.flatMap((requirement) => requirement.steps);
}

declare global {
  interface HTMLElementTagNameMap {
    "idenqa-capture": IdenqaCaptureElement;
  }
}
