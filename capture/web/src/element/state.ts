import type { TemplateResult, nothing } from "lit";
import type { DocumentFrameObserver } from "../document-capture.js";
import type {
  DocumentCaptureGate,
  DocumentCaptureGateResult,
  DocumentDetection,
  DocumentQuad,
} from "../document-detection.js";
import type { CaptureMethodAdapterProgress } from "../method-adapter.js";

export type ElementTemplate = TemplateResult | typeof nothing;

export interface StepUploadState {
  readonly status: "reviewing" | "uploading" | "error" | "accepted";
  readonly body?: Blob;
  readonly previewUrl?: string;
  readonly message?: string;
}

export interface StepCameraState {
  readonly status: "requesting" | "streaming" | "reviewing" | "uploading" | "error" | "accepted";
  readonly stream?: MediaStream;
  readonly ready?: boolean;
  readonly body?: Blob;
  readonly previewUrl?: string;
  readonly width?: number;
  readonly height?: number;
  readonly message?: string;
}

export interface StepCompletion {
  readonly acquisitionMethod: string;
  readonly uploadId: string;
  readonly evidenceId: string;
}

export interface StepDocumentState {
  readonly status: DocumentCaptureGateResult["status"];
  readonly reason?: DocumentCaptureGateResult["reason"];
  readonly progress: number;
  readonly quad?: DocumentQuad;
  readonly frameWidth?: number;
  readonly frameHeight?: number;
}

export interface StepDocumentObservation {
  interval: ReturnType<typeof globalThis.setInterval> | undefined;
  autoCaptureTimer: ReturnType<typeof globalThis.setTimeout> | undefined;
  readonly observer: DocumentFrameObserver;
  readonly gate: DocumentCaptureGate;
  detection: DocumentDetection | undefined;
}

export interface StepAdapterState {
  readonly status: "running" | "error";
  readonly controller: AbortController;
  readonly progress?: CaptureMethodAdapterProgress;
  readonly previewStream?: MediaStream;
  readonly message?: string;
}

export type ComponentFlowState =
  "idle" | "loading" | "ready" | "responding" | "error" | "cancelled";

export type CountryJourneyPhase = "intro" | "notice" | "country";

export type JourneyPhase =
  "intro" | "notice" | "recovery" | "capture" | "confirmation" | "processing" | "complete";

export type StepStage = "method" | "preparation" | "capture";
