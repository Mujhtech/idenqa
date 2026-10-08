import type {
  CaptureRealtimeEvent,
  ExperienceResolution,
  SubjectResponseAction,
} from "@idenqa/sdk";
import type { CaptureDocumentCaptureOptions } from "../document-capture.js";
import type { CaptureFlowStartOptions } from "../flow.js";
import type { CaptureMessageCatalogue } from "../localisation.js";
import type { CaptureMethodAdapter } from "../method-adapter.js";
import type { CaptureRuntimeFallbackReason } from "../planner.js";

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
