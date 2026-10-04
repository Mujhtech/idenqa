import { LitElement, css, html, svg, nothing, type PropertyValues } from "lit";

import type {
  CaptureJourneyAction,
  CaptureJourneyEventType,
  CaptureJourneyScreen,
  CaptureRealtimeEvent,
  ExperienceResolution,
  SubjectResponseAction,
} from "@idenqa/sdk";
import type { EvidenceUpload } from "@idenqa/sdk";

import { installCaptureFont } from "./font.js";

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

export interface CaptureCountryOption {
  /** ISO 3166-1 alpha-2 code. The host must provide only policy-approved countries. */
  readonly code: string;
  /** Subject-facing country name in the host's selected locale. */
  readonly label: string;
}

export interface CaptureCountryJourneyNotice {
  readonly locale: string;
  readonly controller: string;
  readonly recipient: string;
  readonly copy: {
    readonly title: string;
    readonly summary: string;
    readonly purpose: string;
    readonly consequences: string;
  };
  readonly consentRequired: boolean;
}

export interface CaptureCountryJourneyOptions {
  readonly countries: readonly CaptureCountryOption[];
  /** The exact notice the country-bound document session must return. */
  readonly notice: CaptureCountryJourneyNotice;
  /** Optional host-known count for the pre-session introduction. */
  readonly captureItemCount?: number;
  /**
   * Resolves the selected country into a newly-created, immutable capture
   * session. Tenant credentials remain at the host/server boundary.
   */
  readonly resolve: (
    country: CaptureCountryOption,
    signal: AbortSignal,
  ) => Promise<CaptureElementStartOptions>;
  /** Optional package-copy translations shown before the session exists. */
  readonly messageCatalogue?: CaptureMessageCatalogue;
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
type CountryJourneyPhase = "intro" | "notice" | "country";
type JourneyPhase =
  "intro" | "notice" | "recovery" | "capture" | "confirmation" | "processing" | "complete";
type StepStage = "method" | "preparation" | "capture";

export class IdenqaCaptureElement extends LitElement {
  static override properties = {
    plan: { attribute: false },
  };

  static override styles = css`
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

  declare plan: CapturePlan | undefined;

  /** Signed portable-experience presentation applied to this journey, if any. */
  get experience(): CaptureExperiencePresentation | undefined {
    return this.#experience;
  }

  readonly #selectedMethods = new Map<string, string>();
  #documentChoices: Readonly<
    Record<string, { readonly options: readonly { readonly id: string; readonly label: string }[] }>
  > = {};
  readonly #selectedDocuments = new Map<string, string>();
  #choosingDocument: string | undefined;
  #savingDocument = false;
  #documentSelectionError = false;
  readonly #uploadStates = new Map<string, StepUploadState>();
  readonly #cameraStates = new Map<string, StepCameraState>();
  readonly #adapterStates = new Map<string, StepAdapterState>();
  readonly #completedSteps = new Map<string, StepCompletion>();
  #methodAdapters: readonly CaptureMethodAdapter[] = [];
  #flowController: CaptureFlowController | undefined;
  #flowSnapshot: CaptureFlowSnapshot | undefined;
  #flowState: ComponentFlowState = "idle";
  #flowAbortController: AbortController | undefined;
  #countryJourney: CaptureCountryJourneyOptions | undefined;
  #countryJourneyPhase: CountryJourneyPhase = "intro";
  #countryQuery = "";
  #countrySelecting: string | undefined;
  #countrySelectionError = false;
  #countryAbortController: AbortController | undefined;
  #responseError = false;
  #captureCompleteDispatched = false;
  #outcomePolling = false;
  #localizer: CaptureLocalizer = createCaptureLocalizer(browserLocale());
  #experience: CaptureExperiencePresentation | undefined;
  #journeyPhase: JourneyPhase = "intro";
  #stepStage: StepStage = "method";
  #activeStepIndex = 0;
  #lastJourneyScreen: CaptureJourneyScreen | undefined;
  readonly #documentStates = new Map<string, StepDocumentState>();
  readonly #documentObservations = new Map<string, StepDocumentObservation>();
  readonly #capturingSteps = new Set<string>();
  #documentCapture: NormalizedCaptureDocumentCaptureOptions =
    normalizeCaptureDocumentCaptureOptions();

  /**
   * Presents privacy and country selection for a document flow before a
   * verification session exists. Flows without documents use start directly.
   * Selecting a country delegates session creation to the host,
   * verifies the returned notice, records the accepted response, and then
   * continues with the ordinary immutable capture flow.
   */
  startCountryJourney(options: CaptureCountryJourneyOptions): void {
    const countries = normalizeCountryOptions(options.countries);
    if (
      options.captureItemCount !== undefined &&
      (!Number.isInteger(options.captureItemCount) ||
        options.captureItemCount < 1 ||
        options.captureItemCount > 99)
    ) {
      throw new TypeError("Capture item count must be an integer between 1 and 99.");
    }
    this.cancel();
    this.#countryJourney = { ...options, countries };
    this.#countryJourneyPhase = "intro";
    this.#countryQuery = "";
    this.#countrySelecting = undefined;
    this.#countrySelectionError = false;
    this.#localizer = createCaptureLocalizer(browserLocale(), options.messageCatalogue);
    this.#flowState = "idle";
    this.requestUpdate();
  }

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
    applyCaptureExperienceTheme(this, presentation?.theme);
    const catalogue = mergeCaptureMessageCatalogues(
      presentation?.messageCatalogue,
      messageCatalogue,
    );
    const documentCaptureOptions = normalizeCaptureDocumentCaptureOptions(documentCapture);
    this.#localizer = createCaptureLocalizer(presentation?.locale ?? browserLocale(), catalogue);
    this.#documentCapture = documentCaptureOptions;
    this.#documentChoices = {};
    this.#selectedDocuments.clear();
    this.#choosingDocument = undefined;
    this.#savingDocument = false;
    this.#documentSelectionError = false;
    this.#clearCameraStates();
    this.#clearAdapterStates();
    this.#flowAbortController?.abort();
    this.#countryAbortController?.abort();
    this.#countryAbortController = undefined;
    this.#countryJourney = undefined;
    const abortController = new AbortController();
    this.#flowAbortController = abortController;
    this.#flowController = createCaptureFlowController(flowOptions);
    this.#methodAdapters = [];
    this.#flowSnapshot = undefined;
    this.#flowState = "loading";
    this.#journeyPhase = "intro";
    this.#stepStage = "method";
    this.#activeStepIndex = 0;
    this.#lastJourneyScreen = undefined;
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
    this.#countryAbortController?.abort();
    this.#countryAbortController = undefined;
    this.#countryJourney = undefined;
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

  protected override updated(changedProperties: PropertyValues<this>): void {
    super.updated(changedProperties);
    const screen = this.#currentJourneyScreen();
    if (screen === undefined || screen === this.#lastJourneyScreen) return;
    this.#lastJourneyScreen = screen;
    this.#recordJourney("screen_viewed", screen);
    const stateEvent: Partial<Record<CaptureJourneyScreen, CaptureJourneyEventType>> = {
      recovery: "recovery_started",
      processing: "processing_started",
      completion: "completion_shown",
      error: "error_shown",
    };
    const eventType = stateEvent[screen];
    if (eventType !== undefined) this.#recordJourney(eventType, screen);
  }

  protected override render() {
    return html`
      <section
        class=${this.#isDocumentCameraScreen() || this.#isLiveCameraScreen() ? "shell document-camera-shell" : "shell"}
        aria-labelledby="capture-title"
        lang=${this.#localizer.locale}
        dir=${this.#localizer.direction}
      >
        <h1 class="visually-hidden" id="capture-title">${this.#text("identityVerification")}</h1>
        ${
          this.#countryJourney !== undefined
            ? this.#renderCountryJourney()
            : this.#flowState === "loading"
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

  #renderCountryJourney() {
    const journey = this.#countryJourney;
    if (journey === undefined) return nothing;
    if (this.#countryJourneyPhase === "intro") {
      return html`
        <div class="screen preflight-intro">
          <span class="hero-icon" aria-hidden="true">${this.#shieldIcon()}</span>
          <div>
            <p class="eyebrow">${this.#text("secureCapture")}</p>
            <h2>${this.#text("introTitle")}</h2>
            <p class="screen-copy">${this.#text("introBody")}</p>
          </div>
          <ul class="benefits">
            <li>
              ${
                journey.captureItemCount === undefined
                  ? this.#text("countryMatchedDocuments")
                  : this.#text("introStepCount", {
                      count: formatNumber(journey.captureItemCount, this.#localizer.locale),
                    })
              }
            </li>
            <li>${this.#text("introPrivate")}</li>
            <li>${this.#text("introDevice")}</li>
          </ul>
          <div class="journey-actions">
            <button
              class="primary"
              type="button"
              @click=${() => {
                this.#countryJourneyPhase = "notice";
                this.requestUpdate();
              }}
            >
              ${this.#text("getStarted")}
            </button>
          </div>
          ${this.#renderSecuredBy()}
        </div>
      `;
    }

    if (this.#countryJourneyPhase === "notice") return this.#renderCountryJourneyNotice(journey);

    const query = this.#countryQuery.trim().toLocaleLowerCase(this.#localizer.locale);
    const countries = journey.countries.filter(
      (country) =>
        query.length === 0 ||
        country.label.toLocaleLowerCase(this.#localizer.locale).includes(query) ||
        country.code.toLocaleLowerCase(this.#localizer.locale).includes(query),
    );
    return html`
      <div class="screen country-screen">
        ${
          journey.captureItemCount === undefined
            ? nothing
            : html`
                <div class="journey-progress">
                  <p>
                    ${this.#text("stepOf", {
                      current: formatNumber(1, this.#localizer.locale),
                      total: formatNumber(journey.captureItemCount, this.#localizer.locale),
                    })}
                  </p>
                  <p>
                    ${this.#text("progressPercent", { percent: formatNumber(0, this.#localizer.locale) })}
                  </p>
                  <progress
                    aria-label=${this.#text("progressLabel")}
                    value="0"
                    max="100"
                  ></progress>
                </div>
              `
        }
        <div>
          <h2>${this.#text("chooseCountryTitle")}</h2>
          <p class="screen-copy">${this.#text("chooseCountryBody")}</p>
        </div>
        <div class="country-picker">
          <label class="visually-hidden" for="country-search">${this.#text("searchCountry")}</label>
          <span class="country-search-icon" aria-hidden="true">${this.#searchIcon()}</span>
          <input
            id="country-search"
            type="search"
            autocomplete="country-name"
            placeholder=${this.#text("searchCountry")}
            .value=${this.#countryQuery}
            ?disabled=${this.#countrySelecting !== undefined}
            @input=${(event: InputEvent) => {
              this.#countryQuery = (event.currentTarget as HTMLInputElement).value;
              this.#countrySelectionError = false;
              this.requestUpdate();
            }}
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
                ?disabled=${this.#countrySelecting !== undefined}
                @click=${() => void this.#selectCountry(country)}
              >
                <span>${country.label}</span>
                <span class="country-code">${country.code}</span>
                <span class="chevron" aria-hidden="true">›</span>
              </button>
            `,
          )}
          ${
            countries.length === 0
              ? html`<p class="empty-state">${this.#text("countryNoResults")}</p>`
              : nothing
          }
        </div>
        ${
          this.#countrySelecting === undefined
            ? nothing
            : html`<p class="status" role="status">${this.#text("preparingCountry")}</p>`
        }
        ${
          this.#countrySelectionError
            ? html`<p class="error" role="alert">${this.#text("countrySelectionFailed")}</p>`
            : nothing
        }
        <div class="journey-actions">
          <button
            class="quiet"
            type="button"
            ?disabled=${this.#countrySelecting !== undefined}
            @click=${() => {
              this.#countryJourneyPhase = "notice";
              this.#countryQuery = "";
              this.#countrySelectionError = false;
              this.requestUpdate();
            }}
          >
            ${this.#text("back")}
          </button>
        </div>
      </div>
    `;
  }

  #renderCountryJourneyNotice(journey: CaptureCountryJourneyOptions) {
    const { notice } = journey;
    return html`
      <div class="screen notice-screen">
        <div>
          <p class="eyebrow">${this.#text("noticeStep")}</p>
          <h2>${this.#text("noticeTitle")}</h2>
          <p class="screen-copy">${this.#text("noticeBody")}</p>
        </div>
        <article class="notice" aria-labelledby="idq-notice-title" lang=${notice.locale}>
          <div>
            <h4 id="idq-notice-title">${notice.copy.title}</h4>
            <p class="notice-copy">${notice.copy.summary}</p>
          </div>
          <div>
            <h4>${this.#text("whyInformationNeeded")}</h4>
            <p class="notice-copy">${notice.copy.purpose}</p>
          </div>
          <div>
            <h4>${this.#text("ifYouRefuse")}</h4>
            <p class="notice-copy">${notice.copy.consequences}</p>
          </div>
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
          <p class="notice-guidance">
            ${notice.consentRequired ? this.#text("reviewConsent") : this.#text("reviewAcknowledgement")}
          </p>
        </article>
        <div
          class="journey-actions notice-actions"
          role="group"
          aria-label=${this.#text("noticeActionsLabel")}
        >
          <button
            class="primary"
            type="button"
            @click=${() => {
              this.#countryJourneyPhase = "country";
              this.requestUpdate();
            }}
          >
            ${notice.consentRequired ? this.#text("agreeAndContinue") : this.#text("acknowledgeAndContinue")}
          </button>
          <button class="quiet" type="button" @click=${() => this.cancel()}>
            ${this.#text("cancel")}
          </button>
        </div>
      </div>
    `;
  }

  async #selectCountry(country: CaptureCountryOption): Promise<void> {
    const journey = this.#countryJourney;
    if (journey === undefined || this.#countrySelecting !== undefined) return;
    const controller = new AbortController();
    this.#countryAbortController?.abort();
    this.#countryAbortController = controller;
    this.#countrySelecting = country.code;
    this.#countrySelectionError = false;
    this.requestUpdate();
    try {
      const options = await journey.resolve(country, controller.signal);
      if (controller.signal.aborted || this.#countryAbortController !== controller) return;
      const snapshot = await this.start(options);
      if (
        !isActiveCaptureFlowSnapshot(snapshot) ||
        snapshot.status !== "notice_required" ||
        !countryJourneyNoticeMatches(journey.notice, snapshot)
      ) {
        throw new Error("The country-bound session did not return the accepted notice.");
      }
      await this.#respond(journey.notice.consentRequired ? "consent" : "acknowledge");
    } catch (error) {
      if (controller.signal.aborted) return;
      this.#countryAbortController = undefined;
      this.#countrySelecting = undefined;
      this.#countrySelectionError = true;
      this.requestUpdate();
      void error;
    }
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
    const totalSteps = flow.plan.requirements.length;
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
        </div>
        ${this.#renderSecuredBy()}
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
          <h4 id="idq-notice-title">${notice.copy.title}</h4>
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
              ? html`<p class="notice-guidance">
                  ${
                    authority.consentRequired
                      ? this.#text("reviewConsent")
                      : this.#text("reviewAcknowledgement")
                  }
                </p>`
              : html`<p class="notice-guidance" role="status">
                  ${
                    latestResponse?.action === "consent"
                      ? this.#text("consentRecorded")
                      : this.#text("acknowledgementRecorded")
                  }
                </p>`
          }
        </article>
        ${
          flow.status === "notice_required"
            ? this.#renderNoticeActions(authority.consentRequired)
            : this.#journeyPhase === "notice" && flow.status === "capture_ready"
              ? html`<div class="journey-actions">
                  <button
                    class="primary"
                    type="button"
                    @click=${() => this.#resumeJourney(flow.plan)}
                  >
                    ${this.#text("continue")}
                  </button>
                </div>`
              : nothing
        }
      </div>
    `;
  }

  #renderNoticeActions(consentRequired: boolean) {
    const busy = this.#flowState === "responding";
    const acceptedAction: SubjectResponseAction = consentRequired ? "consent" : "acknowledge";
    return html`
      <div
        class="journey-actions notice-actions"
        role="group"
        aria-label=${this.#text("noticeActionsLabel")}
      >
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
        <button
          class="quiet"
          type="button"
          ?disabled=${busy}
          @click=${() => void this.#respond("refuse")}
        >
          ${this.#text("cancel")}
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
    const activeRequirementIndex = plan.requirements.findIndex(
      (requirement) => requirement.key === step.requirementKey,
    );
    const completedRequirements = plan.requirements.filter((requirement) =>
      requirement.steps.every((candidate) => this.#completedSteps.has(stepKey(candidate))),
    ).length;
    const totalRequirements = plan.requirements.length;
    const item = friendlyArtefact(step.artefact, this.#localizer);
    return html`
      <div class="screen">
        <div class="journey-progress">
          <p>
            ${this.#text("stepOf", {
              current: formatNumber(activeRequirementIndex + 1, locale),
              total: formatNumber(totalRequirements, locale),
            })}
          </p>
          <p>
            ${this.#text("progressPercent", { percent: formatNumber(Math.round((completedRequirements / totalRequirements) * 100), locale) })}
          </p>
          <progress
            aria-label=${this.#text("progressLabel")}
            value=${completedRequirements}
            max=${totalRequirements}
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
          isDocumentArtefact(step.artefact) &&
          this.#documentChoices[step.requirementKey] !== undefined &&
          (!this.#selectedDocuments.has(step.requirementKey) ||
            this.#choosingDocument === step.requirementKey)
            ? this.#renderDocumentChoice(step)
            : this.#stepStage === "method"
              ? this.#renderMethodChoice(step, item)
              : this.#stepStage === "preparation"
                ? this.#renderPreparation(step, item)
                : this.#renderCaptureTask(step, item)
        }
      </div>
    `;
  }

  #isDocumentCameraScreen(): boolean {
    const flow = this.#flowSnapshot;
    if (
      flow === undefined ||
      !isActiveCaptureFlowSnapshot(flow) ||
      flow.status !== "capture_ready" ||
      this.#journeyPhase !== "capture" ||
      this.#stepStage !== "capture"
    )
      return false;
    const step = planSteps(flow.plan)[this.#activeStepIndex];
    return (
      step !== undefined &&
      isDocumentArtefact(step.artefact) &&
      (this.#selectedMethods.get(stepKey(step)) ?? step.methodOptions[0]) === LIVE_CAMERA_METHOD
    );
  }

  #isLiveCameraScreen(): boolean {
    const flow = this.#flowSnapshot;
    if (
      flow === undefined ||
      !isActiveCaptureFlowSnapshot(flow) ||
      flow.status !== "capture_ready" ||
      this.#journeyPhase !== "capture" ||
      this.#stepStage !== "capture"
    )
      return false;
    const step = planSteps(flow.plan)[this.#activeStepIndex];
    if (step === undefined) return false;
    const method = this.#selectedMethods.get(stepKey(step)) ?? step.methodOptions[0];
    return (
      method === LIVE_CAMERA_METHOD &&
      step.evidenceType === "idenqa.evidence.selfie_image" &&
      step.artefact === "idenqa.artefact.selfie_image" &&
      this.#adapterFor(step, method)?.presentation === "active_liveness"
    );
  }

  #renderDocumentChoice(step: CapturePlanStep) {
    if (
      this.#documentChoices[step.requirementKey]?.options.length === 1 &&
      !this.#documentSelectionError
    ) {
      return html`<p role="status">${this.#text("savingDocument")}</p>
        ${this.#renderJourneyExit()}`;
    }
    return html`
      <div>
        <h2>${this.#text("chooseDocumentTitle")}</h2>
        <p class="screen-copy">${this.#text("chooseDocumentBody")}</p>
      </div>
      <div class="method-list">
        ${this.#documentChoices[step.requirementKey]!.options.map(
          (option) => html`
            <button
              class="method-card"
              type="button"
              ?disabled=${this.#savingDocument}
              aria-pressed=${String(this.#selectedDocuments.get(step.requirementKey) === option.id)}
              @click=${() => void this.#selectDocument(step, option.id)}
            >
              <span class="method-icon" aria-hidden="true">${this.#documentIcon()}</span>
              <span>${option.label}</span><span class="chevron" aria-hidden="true">›</span>
            </button>
          `,
        )}
      </div>
      ${this.#savingDocument ? html`<p role="status">${this.#text("savingDocument")}</p>` : nothing}
      ${this.#documentSelectionError ? html`<p class="error" role="alert">${this.#text("documentSelectionFailed")}</p>` : nothing}
      ${
        this.#choosingDocument === step.requirementKey
          ? html`
              <button
                type="button"
                ?disabled=${this.#savingDocument}
                @click=${() => {
                  this.#choosingDocument = undefined;
                  this.#documentSelectionError = false;
                  this.requestUpdate();
                }}
              >
                ${this.#text("back")}
              </button>
            `
          : nothing
      }
      ${this.#renderJourneyExit()}
    `;
  }

  async #selectDocument(step: CapturePlanStep, documentType: string): Promise<void> {
    if (this.#savingDocument || this.#flowController === undefined) return;
    const controller = this.#flowController;
    const abort = this.#flowAbortController;
    this.#savingDocument = true;
    this.#documentSelectionError = false;
    this.requestUpdate();
    try {
      const snapshot = await controller.selectDocument(
        step.requirementKey,
        documentType,
        abort?.signal,
      );
      if (this.#flowController !== controller || abort?.signal.aborted) return;
      this.#flowSnapshot = snapshot;
      this.#restoreRecoveredProgress(snapshot);
      this.#choosingDocument = undefined;
      if (isActiveCaptureFlowSnapshot(snapshot)) {
        this.#enterNextIncompleteStep(snapshot.plan, 0);
      } else if (snapshot.status === "processing" || snapshot.status === "action_required") {
        this.#pollAuthoritativeOutcome();
      }
      const confirmedType = isActiveCaptureFlowSnapshot(snapshot)
        ? snapshot.session.documentSelections?.[step.requirementKey]
        : undefined;
      if (confirmedType !== undefined) {
        this.dispatchEvent(
          new CustomEvent("idenqa:document-selected", {
            bubbles: true,
            composed: true,
            detail: { requirementKey: step.requirementKey, documentType: confirmedType },
          }),
        );
      }
    } catch {
      if (this.#flowController === controller && !abort?.signal.aborted)
        this.#documentSelectionError = true;
    } finally {
      if (this.#flowController === controller) {
        this.#savingDocument = false;
        this.requestUpdate();
      }
    }
  }

  #shieldIcon() {
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

  #searchIcon() {
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

  #renderSecuredBy() {
    return html`<p class="secured-by">
      <span>${this.#text("securedBy")}</span><span class="mini-mark" aria-hidden="true">I</span>
    </p>`;
  }

  #documentIcon() {
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

  #documentItem(step: CapturePlanStep): string {
    const selected = this.#documentChoices[step.requirementKey]?.options.find(
      (option) => option.id === this.#selectedDocuments.get(step.requirementKey),
    );
    return this.#text(
      step.artefact.endsWith("document_back") ? "documentBackOf" : "documentFrontOf",
      {
        document: selected?.label ?? this.#text("identityDocument"),
      },
    );
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
          ? html`<ul class="benefits">
              <li>${this.#text("tipLighting")}</li>
              <li>${this.#text("tipReadable")}</li>
              <li>${this.#text("tipPrivacy")}</li>
            </ul>`
          : copy.tips.length === 0
            ? nothing
            : html`<ul class="benefits">
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
          ${
            this.#canChangeDocument(step) || step.methodOptions.length === 1
              ? this.#text("back")
              : this.#text("chooseAnotherMethod")
          }
        </button>
      </div>
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
    if (
      method === LIVE_CAMERA_METHOD &&
      adapter === undefined &&
      isDocumentArtefact(step.artefact)
    ) {
      const documentBusy = cameraState?.status === "uploading" || this.#capturingSteps.has(key);
      return html`
        <div class="document-navigation">
          <button
            type="button"
            ?disabled=${documentBusy}
            @click=${() => this.#backToPreparation(step)}
          >
            ${this.#text("back")}
          </button>
          <button type="button" ?disabled=${documentBusy} @click=${() => this.cancel()}>
            ${this.#text("cancel")}
          </button>
        </div>
        ${this.#renderCamera(step, `guided-${this.#activeStepIndex}`, cameraState, false)}
      `;
    }
    return html`
      <div class="document-navigation">
        <button
          type="button"
          ?disabled=${uploadState?.status === "uploading" || isCameraBusy(cameraState)}
          @click=${() => this.#backToPreparation(step)}
        >
          ${this.#text("back")}
        </button>
        <button class="quiet" type="button" ?disabled=${busy} @click=${() => this.cancel()}>
          ${this.#text("cancel")}
        </button>
      </div>

      <div class="document-camera">
        <h2 class="document-instruction">
          ${copy?.instruction ?? captureInstruction(method, this.#localizer)}
        </h2>
        <!--<h2>${this.#text("captureTitle", { item })}</h2>
        <p class="screen-copy">
          ${copy?.instruction ?? captureInstruction(method, this.#localizer)}
        </p>-->
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
    const progress = captureProgress(plan, this.#completedSteps);
    const activeRequirementIndex = plan.requirements.findIndex(
      (requirement) => requirement.key === step?.requirementKey,
    );
    return html`
      <div class="screen" aria-live="polite">
        <div class="journey-progress">
          <p>
            ${this.#text("stepOf", {
              current: formatNumber(
                Math.min(activeRequirementIndex + 1, progress.totalSteps),
                this.#localizer.locale,
              ),
              total: formatNumber(progress.totalSteps, this.#localizer.locale),
            })}
          </p>
          <p>
            ${this.#text("progressPercent", {
              percent: formatNumber(
                Math.round((progress.completedSteps / progress.totalSteps) * 100),
                this.#localizer.locale,
              ),
            })}
          </p>
          <progress
            aria-label=${this.#text("progressLabel")}
            value=${progress.completedSteps}
            max=${progress.totalSteps}
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
    this.#recordJourney("action_selected", "intro", "continue");
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
    if (showRecovery && this.#completedSteps.size > 0) {
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
    this.#stepStage =
      isDocumentArtefact(step.artefact) &&
      (this.#documentChoices[step.requirementKey]?.options.length ?? 0) > 1 &&
      step.methodOptions.length > 1 &&
      !steps.some(
        (candidate) =>
          candidate.requirementKey === step.requirementKey &&
          this.#completedSteps.has(stepKey(candidate)),
      )
        ? "method"
        : "preparation";
    this.#journeyPhase = "capture";
    const options = this.#documentChoices[step.requirementKey]?.options;
    if (options?.length === 1 && !this.#selectedDocuments.has(step.requirementKey)) {
      void this.#selectDocument(step, options[0]!.id);
    }
    this.requestUpdate();
  }

  #chooseGuidedMethod(step: CapturePlanStep, method: string): void {
    this.#selectMethod(step, method);
    this.#recordStepJourney("action_selected", "method", step, "select_method", method);
    this.#stepStage = "preparation";
    this.requestUpdate();
  }

  #canChangeDocument(step: CapturePlanStep): boolean {
    const flow = this.#flowSnapshot;
    return (
      isDocumentArtefact(step.artefact) &&
      (this.#documentChoices[step.requirementKey]?.options.length ?? 0) > 1 &&
      flow !== undefined &&
      isActiveCaptureFlowSnapshot(flow) &&
      !planSteps(flow.plan).some(
        (candidate) =>
          candidate.requirementKey === step.requirementKey &&
          this.#completedSteps.has(stepKey(candidate)),
      )
    );
  }

  #backFromPreparation(step: CapturePlanStep): void {
    this.#recordStepJourney("navigation_back", "preparation", step, "back");
    if (this.#canChangeDocument(step)) {
      this.#choosingDocument = step.requirementKey;
      this.#documentSelectionError = false;
    } else if (step.methodOptions.length > 1) {
      this.#selectedMethods.delete(stepKey(step));
      this.#stepStage = "method";
    } else {
      this.#journeyPhase = "notice";
    }
    this.requestUpdate();
  }

  #showCaptureTask(): void {
    const step = this.#activeJourneyStep();
    if (step !== undefined) {
      this.#recordStepJourney("capture_started", "preparation", step, "start_capture");
    }
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
    this.#recordStepJourney("navigation_back", "capture", step, "back");
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
    if (isDocumentArtefact(step.artefact)) {
      return this.#renderDocumentCamera(step, idSuffix, state, lockedByFile, documentCaptureActive);
    }
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

  #renderDocumentCamera(
    step: CapturePlanStep,
    idSuffix: string,
    state: StepCameraState | undefined,
    lockedByFile: boolean,
    detectionEnabled: boolean,
  ) {
    const key = stepKey(step);
    const videoId = `idq-camera-${idSuffix}`;
    const reviewing = state?.previewUrl !== undefined;
    const uploading = state?.status === "uploading";
    const streaming = state?.status === "streaming";
    const documentState = this.#documentStates.get(key);
    return html`
      <div class="document-camera camera-option" data-state=${state?.status ?? "idle"}>
        <h2 class="document-instruction">
          ${
            reviewing
              ? this.#text("documentReviewInstruction")
              : this.#text("documentCaptureInstruction", { item: this.#documentItem(step) })
          }
        </h2>
        <div class="document-viewfinder">
          <div class="document-preview-frame" data-document-capture=${String(detectionEnabled)}>
            ${
              reviewing
                ? html`
                    <img
                      class="camera-preview"
                      src=${state.previewUrl!}
                      alt=${this.#text("capturedPreviewLabel", { artefact: this.#selectedDocuments.has(step.requirementKey) ? this.#documentItem(step) : friendlyArtefact(step.artefact, this.#localizer) })}
                      width=${state.width ?? 640}
                      height=${state.height ?? 480}
                    />
                  `
                : streaming
                  ? html`
                      <video
                        class="camera-preview"
                        id=${videoId}
                        width="640"
                        height="480"
                        autoplay
                        muted
                        playsinline
                        aria-label=${this.#text("liveCameraPreviewLabel", { artefact: this.#selectedDocuments.has(step.requirementKey) ? this.#documentItem(step) : friendlyArtefact(step.artefact, this.#localizer) })}
                      ></video>
                      ${detectionEnabled ? this.#renderDocumentGuide(state, documentState) : nothing}
                    `
                  : html`<div class="document-camera-placeholder" aria-hidden="true">
                      ${this.#documentIcon()}
                    </div>`
            }
          </div>
          <div class="document-side-label">
            ${this.#documentIcon()}<span
              >${this.#text(step.artefact.endsWith("document_back") ? "documentBackLabel" : "documentFrontLabel")}</span
            >
          </div>
        </div>
        <p class="document-feedback" role="status" aria-live="polite">
          ${
            uploading
              ? this.#text("uploadingCapturedPhoto")
              : state?.status === "requesting"
                ? this.#text("waitingForCameraPermission")
                : streaming
                  ? state.ready !== true
                    ? this.#text("startingCameraPreview")
                    : detectionEnabled
                      ? documentHintMessage(documentState, this.#localizer)
                      : this.#text("cameraReady")
                  : nothing
          }
        </p>
        ${
          streaming && detectionEnabled && this.#documentCapture.autoCapture
            ? html`<p class="document-auto-copy">${this.#text("documentAutoCapture")}</p>`
            : nothing
        }
        ${
          !reviewing
            ? html` <details class="document-help">
                <summary>${this.#text("documentHelp")}</summary>
                <p>${this.#text("documentHelpBody")}</p>
              </details>`
            : nothing
        }
        ${state?.status === "error" ? html`<p class="error" role="alert">${state.message}</p>` : nothing}
        <div class="document-controls camera-actions">
          ${
            reviewing
              ? html`
                  <button
                    class="primary"
                    type="button"
                    ?disabled=${uploading || state.body === undefined}
                    @click=${() => void this.#uploadCamera(step, state.body!)}
                  >
                    ${this.#text(uploading ? "uploadingPhoto" : "usePhoto")}
                  </button>
                  <button
                    type="button"
                    ?disabled=${uploading}
                    @click=${() => void this.#retakePhoto(step, idSuffix)}
                  >
                    ${this.#text("retakePhoto")}
                  </button>
                `
              : streaming
                ? html`
                    <button
                      class="document-shutter"
                      type="button"
                      ?disabled=${state.ready !== true || this.#capturingSteps.has(key)}
                      @click=${() => void this.#capturePhoto(step, videoId, this.#documentObservations.get(key)?.detection)}
                    >
                      <span class="shutter-icon" aria-hidden="true"></span
                      >${this.#text("capturePhoto")}
                    </button>
                    ${
                      this.#isDocumentCameraScreen()
                        ? nothing
                        : html`<button type="button" @click=${() => this.#cancelCamera(step)}>
                            ${this.#text("cancelCamera")}
                          </button>`
                    }
                  `
                : html`
                    <button
                      class="primary"
                      type="button"
                      ?disabled=${lockedByFile || state?.status === "requesting"}
                      @click=${() => void this.#startCamera(step, idSuffix)}
                    >
                      ${this.#text(
                        lockedByFile
                          ? "fileUploadInProgress"
                          : state?.status === "requesting"
                            ? "requestingCamera"
                            : state?.status === "error"
                              ? "retryCamera"
                              : "startCamera",
                      )}
                    </button>
                  `
          }
        </div>
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

  #renderPoseRing(progress: CaptureMethodAdapterProgress | undefined) {
    const prompt = progress?.prompt;
    const stage = poseGuideStage(progress);
    const center =
      prompt === "turn_right"
        ? 0
        : prompt === "turn_left"
          ? Math.PI
          : prompt === "look_up"
            ? -Math.PI / 2
            : Math.PI / 2;
    const arcHalfAngle = Math.PI / 6;
    return html`
      <svg
        class="liveness-face-guide"
        viewBox="0 0 100 100"
        preserveAspectRatio="xMidYMid meet"
        data-stage=${stage}
        aria-hidden="true"
        focusable="false"
      >
        ${[0, Math.PI / 2, Math.PI, -Math.PI / 2].map((arcCenter) => {
          const active = stage === "pose" && (prompt === "blink" || arcCenter === center);
          if (active) {
            return Array.from({ length: 29 }, (_, index) => {
              const angle = arcCenter - arcHalfAngle + (index / 28) * 2 * arcHalfAngle;
              const filled = Math.abs(index - 14) / 14 < (progress?.poseProgress ?? 0);
              return svg`<line
                x1=${50 + Math.cos(angle) * 40} y1=${50 + Math.sin(angle) * 40}
                x2=${50 + Math.cos(angle) * 46} y2=${50 + Math.sin(angle) * 46}
                data-filled=${String(filled)} />`;
            });
          }
          const start = arcCenter - arcHalfAngle;
          const end = arcCenter + arcHalfAngle;
          return svg`<path class="liveness-guide-arc"
            d=${`M ${50 + Math.cos(start) * 43} ${50 + Math.sin(start) * 43} A 43 43 0 0 1 ${50 + Math.cos(end) * 43} ${50 + Math.sin(end) * 43}`} />`;
        })}
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
    const challengeProgress =
      state?.progress?.phase === "challenge"
        ? this.#text("challengeProgress", {
            current: formatNumber(state.progress.current!, this.#localizer.locale),
            total: formatNumber(state.progress.total!, this.#localizer.locale),
          })
        : undefined;
    const feedback = state?.progress?.poseFeedback;
    const guideStage = poseGuideStage(state?.progress);
    const guidance =
      guideStage === "centered"
        ? this.#text("poseCentered")
        : feedback !== undefined && feedback !== "follow_prompt"
          ? this.#text(poseFeedbackKey(feedback))
          : guideStage === "centering"
            ? this.#text("poseCenterFace")
            : prompt;
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
                        ${this.#renderPoseRing(state?.progress)}
                        ${
                          prompt === undefined
                            ? nothing
                            : html`<span class="liveness-overlay-prompt" aria-hidden="true"
                                >${guidance}</span
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
                <div
                  class=${activeLiveness ? "adapter-progress liveness-progress-panel" : "adapter-progress"}
                  role="status"
                  aria-live="polite"
                >
                  ${
                    state.progress?.phase === "challenge" && prompt !== undefined
                      ? html`
                          <p
                            class=${activeLiveness ? "liveness-progress-label" : "adapter-challenge-label"}
                          >
                            ${challengeProgress}
                          </p>
                          ${
                            activeLiveness
                              ? html`
                                  <div
                                    class="liveness-segments"
                                    role="progressbar"
                                    aria-label=${this.#text("livenessProgressLabel")}
                                    aria-live="off"
                                    aria-valuemin="1"
                                    aria-valuemax=${state.progress.total!}
                                    aria-valuenow=${state.progress.current!}
                                    aria-valuetext=${challengeProgress!}
                                  >
                                    ${Array.from(
                                      { length: state.progress.total! },
                                      (_, index) => html`
                                        <span
                                          class="liveness-segment"
                                          aria-hidden="true"
                                          data-state=${index + 1 < state.progress!.current! ? "complete" : index + 1 === state.progress!.current! ? "active" : "upcoming"}
                                        ></span>
                                      `,
                                    )}
                                  </div>
                                  <p class="visually-hidden">${guidance}</p>
                                `
                              : html`
                                  <progress
                                    aria-label=${this.#text("livenessProgressLabel")}
                                    aria-live="off"
                                    value=${state.progress.current! - 1 + (state.progress.poseProgress ?? 0)}
                                    max=${state.progress.total!}
                                  ></progress>
                                  ${
                                    state.progress.poseProgress === undefined
                                      ? nothing
                                      : html`<progress
                                          class="liveness-pose-meter"
                                          aria-live="off"
                                          aria-label=${this.#text("poseProgressLabel")}
                                          value=${state.progress.poseProgress}
                                          max="1"
                                        ></progress>`
                                  }
                                `
                          }
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
                  ${
                    activeLiveness && state.progress?.phase === "challenge"
                      ? nothing
                      : html`<p style="font-size:0.75rem">
                          ${adapterProgressMessage(state.progress, copy.label, this.#localizer)}
                        </p>`
                  }
                </div>
                <!--<button type="button" @click=${() => this.#cancelMethodAdapter(step)}>
                  ${this.#text("cancelMethod", { method: copy.label })}
                </button>-->
              `
            : html`
                <button
                  class="primary"
                  type="button"
                  style="margin-left: var(--idq-capture-shell-padding); margin-right: var(--idq-capture-shell-padding);"
                  @click=${() => void this.#runMethodAdapter(step, adapter, previewId)}
                >
                  ${copy.action}
                </button>
              `
        }
        ${
          state?.status === "error"
            ? html`<p
                class="error"
                style="margin-left: var(--idq-capture-shell-padding); margin-right: var(--idq-capture-shell-padding); font-size:0.75rem"
                role="alert"
              >
                ${state.message}
              </p>`
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
          method === LIVE_CAMERA_METHOD &&
          step.evidenceType === "idenqa.evidence.selfie_image" &&
          step.artefact === "idenqa.artefact.selfie_image"
        ) {
          const selfieAdapter = requireCaptureMethodAdapter(this.#methodAdapters, context);
          if (selfieAdapter.presentation !== "active_liveness") {
            throw new CaptureMethodAdapterError(
              "CAPTURE_METHOD_ADAPTER_INVALID",
              "Live selfie capture requires the guided active-liveness presentation.",
            );
          }
          continue;
        }
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
      if (!unconfirmed) {
        try {
          this.#flowSnapshot = flowController.captureFailed(step);
          this.#adapterStates.delete(key);
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
          return;
        } catch {
          // No usable capture_failed fallback exists; keep the adapter retry local.
        }
      }
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
    this.#recordStepJourney("capture_retake", "review", step, "retake", LIVE_CAMERA_METHOD);
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
    this.#recordStepJourney(
      "capture_accepted",
      "review",
      step,
      "accept_capture",
      acquisitionMethod,
    );
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
    const choices: Record<string, { options: readonly { id: string; label: string }[] }> = {};
    this.#selectedDocuments.clear();
    for (const requirement of snapshot.plan.requirements) {
      if (requirement.documentOptions !== undefined)
        choices[requirement.key] = { options: requirement.documentOptions };
      if (requirement.selectedDocument !== undefined)
        this.#selectedDocuments.set(requirement.key, requirement.selectedDocument);
    }
    this.#documentChoices = choices;
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
    this.#recordJourney(
      "action_selected",
      "notice",
      action === "refuse" ? "refuse_notice" : "accept_notice",
    );
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
    this.#recordJourney("error_shown", "error");
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

  #currentJourneyScreen(): CaptureJourneyScreen | undefined {
    if (this.#flowController === undefined && this.#flowSnapshot === undefined) return undefined;
    if (this.#flowState === "error") return "error";
    const flow = this.#flowSnapshot;
    if (flow !== undefined && !isActiveCaptureFlowSnapshot(flow)) {
      if (flow.status === "processing" || flow.status === "action_required") return "processing";
      if (flow.status === "failed") return "error";
      return "completion";
    }
    if (this.#journeyPhase === "confirmation") return "review";
    if (this.#journeyPhase === "complete") return "completion";
    if (this.#journeyPhase !== "capture") return this.#journeyPhase;
    return this.#stepStage;
  }

  #activeJourneyStep(): CapturePlanStep | undefined {
    const flow = this.#flowSnapshot;
    if (flow === undefined || !isActiveCaptureFlowSnapshot(flow)) return undefined;
    return planSteps(flow.plan)[this.#activeStepIndex];
  }

  #recordStepJourney(
    eventType: CaptureJourneyEventType,
    screen: CaptureJourneyScreen,
    step: CapturePlanStep,
    action?: CaptureJourneyAction,
    acquisitionMethod?: string,
  ): void {
    this.#recordJourney(eventType, screen, action, {
      requirementKey: step.requirementKey,
      artefact: step.artefact,
      ...(acquisitionMethod === undefined ? {} : { acquisitionMethod }),
    });
  }

  #recordJourney(
    eventType: CaptureJourneyEventType,
    screen: CaptureJourneyScreen,
    action?: CaptureJourneyAction,
    context: {
      readonly requirementKey?: string;
      readonly artefact?: string;
      readonly acquisitionMethod?: string;
    } = {},
  ): void {
    void this.#flowController
      ?.recordJourneyEvent({
        eventType,
        screen,
        ...(action === undefined ? {} : { action }),
        ...context,
      })
      .catch(() => undefined);
  }
}

export function defineIdenqaCapture(
  tagName: string = IDENQA_CAPTURE_TAG_NAME,
): typeof IdenqaCaptureElement {
  if (typeof customElements === "undefined") {
    throw new Error("Custom elements are not available in this environment.");
  }
  installCaptureFont();
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
  return {
    completedSteps: plan.requirements.filter((requirement) =>
      requirement.steps.every((step) => completedSteps.has(stepKey(step))),
    ).length,
    totalSteps: plan.requirements.length,
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

function poseFeedbackKey(
  feedback: NonNullable<CaptureMethodAdapterProgress["poseFeedback"]>,
): CaptureMessageKey {
  const keys = {
    find_face: "poseFindFace",
    one_face: "poseOneFace",
    center_face: "poseCenterFace",
    move_closer: "poseMoveCloser",
    move_back: "poseMoveBack",
    face_forward: "poseFaceForward",
    follow_prompt: "poseFollowPrompt",
    hold_still: "poseHoldStill",
    open_eyes: "poseOpenEyes",
    quality: "poseQuality",
  } as const;
  return keys[feedback];
}

function poseGuideStage(progress: CaptureMethodAdapterProgress | undefined) {
  if (progress?.poseStage !== undefined) return progress.poseStage;
  // Older host adapters can still render guidance without inventing measured
  // confirmation. Only an explicit centered stage makes the arcs green.
  if (
    progress?.prompt === undefined ||
    progress.prompt === "neutral" ||
    (progress.poseFeedback !== undefined &&
      progress.poseFeedback !== "follow_prompt" &&
      progress.poseFeedback !== "hold_still" &&
      progress.poseFeedback !== "open_eyes")
  ) {
    return "centering";
  }
  return "pose";
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

function normalizeCountryOptions(
  countries: readonly CaptureCountryOption[],
): readonly CaptureCountryOption[] {
  if (countries.length === 0 || countries.length > 249) {
    throw new TypeError("Country selection requires between 1 and 249 approved countries.");
  }
  const seen = new Set<string>();
  return countries.map((country) => {
    const code = country.code.trim().toUpperCase();
    const label = country.label.trim();
    if (!/^[A-Z]{2}$/.test(code) || label.length === 0 || label.length > 100) {
      throw new TypeError("Country options require an ISO alpha-2 code and a display label.");
    }
    if (seen.has(code)) throw new TypeError(`Country option ${code} is duplicated.`);
    seen.add(code);
    return { code, label };
  });
}

function countryJourneyNoticeMatches(
  expected: CaptureCountryJourneyNotice,
  snapshot: CaptureActiveFlowSnapshot,
): boolean {
  const { notice, authority } = snapshot.authoritySnapshot;
  return (
    authority.consentRequired === expected.consentRequired &&
    notice.locale === expected.locale &&
    notice.controller === expected.controller &&
    notice.recipient === expected.recipient &&
    notice.copy.title === expected.copy.title &&
    notice.copy.summary === expected.copy.summary &&
    notice.copy.purpose === expected.copy.purpose &&
    notice.copy.consequences === expected.copy.consequences
  );
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
