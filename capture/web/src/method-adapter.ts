import type { CaptureLivenessPrompt } from "./acquisition.js";
import type { CapturePoseFeedback } from "./pose.js";
import type { CaptureRuntimeFallbackReason } from "./planner.js";

export interface CaptureMethodAdapterContext {
  readonly verificationId: string;
  readonly requirementKey: string;
  readonly evidenceType: string;
  readonly artefact: string;
  readonly acquisitionMethod: string;
  readonly fallbackCondition?: CaptureRuntimeFallbackReason;
  readonly locale: string;
  readonly signal: AbortSignal;
}

export interface CaptureMethodAdapterCopy {
  readonly label: string;
  readonly action: string;
  readonly description: string;
  readonly title?: string;
  readonly preparation: string;
  readonly instruction: string;
  readonly tips?: readonly string[];
}

export type CaptureMethodAdapterCopyResolver =
  CaptureMethodAdapterCopy | ((locale: string) => CaptureMethodAdapterCopy);

export type CaptureMethodAdapterPhase =
  "preparing" | "requesting_permission" | "ready" | "challenge" | "submitting";

export interface CaptureMethodAdapterProgress {
  readonly phase: CaptureMethodAdapterPhase;
  readonly current?: number;
  readonly total?: number;
  readonly prompt?: CaptureLivenessPrompt;
  readonly poseProgress?: number;
  readonly poseFeedback?: CapturePoseFeedback;
}

export interface CaptureMethodAdapterControls {
  /** Reports closed, identifier-free progress. Raw evidence must never enter this value. */
  update(progress: CaptureMethodAdapterProgress): void;
  /**
   * Shows or clears a local preview without serialising it into attributes or events.
   * Passing a stream transfers its preview-track cleanup to Capture Web.
   */
  setPreview(stream: MediaStream | undefined): Promise<void>;
}

export interface CaptureMethodAdapter {
  readonly method: string;
  readonly requirementKey?: string;
  readonly artefact?: string;
  /** Optional package-owned presentation for a known interaction pattern. */
  readonly presentation?: "active_liveness";
  readonly copy: CaptureMethodAdapterCopyResolver;
  /**
   * Performs acquisition and submission. Returning is not success: Capture Web
   * re-reads Core and completes the step only from authoritative progress.
   */
  acquire(
    context: CaptureMethodAdapterContext,
    controls: CaptureMethodAdapterControls,
  ): Promise<void>;
}

export type CaptureMethodAdapterErrorCode =
  | "CAPTURE_METHOD_ADAPTER_INVALID"
  | "CAPTURE_METHOD_ADAPTER_MISSING"
  | "CAPTURE_METHOD_ADAPTER_AMBIGUOUS"
  | "CAPTURE_METHOD_ADAPTER_UNCONFIRMED";

export class CaptureMethodAdapterError extends Error {
  readonly code: CaptureMethodAdapterErrorCode;

  constructor(code: CaptureMethodAdapterErrorCode, message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "CaptureMethodAdapterError";
    this.code = code;
  }
}

export function validateCaptureMethodAdapters(
  adapters: readonly CaptureMethodAdapter[],
): readonly CaptureMethodAdapter[] {
  const result = [...adapters];
  const bindings = new Set<string>();
  for (const [index, adapter] of result.entries()) {
    if (typeof adapter !== "object" || adapter === null || typeof adapter.acquire !== "function") {
      throw invalid(`Adapter ${index} is invalid.`);
    }
    validateRegistryName(adapter.method, "method", `Adapter ${index} method`);
    optionalRequirementKey(adapter.requirementKey, `Adapter ${index} requirementKey`);
    optionalRegistryName(adapter.artefact, "artefact", `Adapter ${index} artefact`);
    if (adapter.presentation !== undefined && adapter.presentation !== "active_liveness") {
      throw invalid(`Adapter ${index} presentation is invalid.`);
    }
    captureMethodAdapterCopy(adapter, "en");
    const binding = JSON.stringify([
      adapter.method,
      adapter.requirementKey ?? null,
      adapter.artefact ?? null,
    ]);
    if (bindings.has(binding)) {
      throw invalid(`Adapter ${index} repeats an existing method binding.`);
    }
    bindings.add(binding);
  }
  return result;
}

export function findCaptureMethodAdapter(
  adapters: readonly CaptureMethodAdapter[],
  context: Omit<CaptureMethodAdapterContext, "locale" | "signal">,
): CaptureMethodAdapter | undefined {
  const matches = adapters.filter(
    (adapter) =>
      adapter.method === context.acquisitionMethod &&
      (adapter.requirementKey === undefined || adapter.requirementKey === context.requirementKey) &&
      (adapter.artefact === undefined || adapter.artefact === context.artefact),
  );
  if (matches.length > 1) {
    throw new CaptureMethodAdapterError(
      "CAPTURE_METHOD_ADAPTER_AMBIGUOUS",
      "More than one acquisition adapter matches the capture step.",
    );
  }
  return matches[0];
}

export function requireCaptureMethodAdapter(
  adapters: readonly CaptureMethodAdapter[],
  context: Omit<CaptureMethodAdapterContext, "locale" | "signal">,
): CaptureMethodAdapter {
  const adapter = findCaptureMethodAdapter(adapters, context);
  if (adapter === undefined) {
    throw new CaptureMethodAdapterError(
      "CAPTURE_METHOD_ADAPTER_MISSING",
      `No acquisition adapter is registered for ${context.acquisitionMethod}.`,
    );
  }
  return adapter;
}

export function captureMethodAdapterCopy(
  adapter: CaptureMethodAdapter,
  locale: string,
): CaptureMethodAdapterCopy {
  const copy = typeof adapter.copy === "function" ? adapter.copy(locale) : adapter.copy;
  if (typeof copy !== "object" || copy === null) throw invalid("Adapter copy is invalid.");
  return {
    label: copyField(copy.label, "label", 120),
    action: copyField(copy.action, "action", 120),
    description: copyField(copy.description, "description", 240),
    ...(copy.title === undefined ? {} : { title: copyField(copy.title, "title", 160) }),
    preparation: copyField(copy.preparation, "preparation", 500),
    instruction: copyField(copy.instruction, "instruction", 500),
    ...(copy.tips === undefined ? {} : { tips: copyTips(copy.tips) }),
  };
}

export function normalizeCaptureMethodProgress(
  progress: CaptureMethodAdapterProgress,
): CaptureMethodAdapterProgress {
  const phases = new Set<CaptureMethodAdapterPhase>([
    "preparing",
    "requesting_permission",
    "ready",
    "challenge",
    "submitting",
  ]);
  if (typeof progress !== "object" || progress === null || !phases.has(progress.phase)) {
    throw invalid("Adapter progress phase is invalid.");
  }
  const hasCount = progress.current !== undefined || progress.total !== undefined;
  if (hasCount) {
    if (
      !Number.isSafeInteger(progress.current) ||
      !Number.isSafeInteger(progress.total) ||
      progress.current! < 1 ||
      progress.total! < 1 ||
      progress.current! > progress.total!
    ) {
      throw invalid("Adapter progress counts are invalid.");
    }
  }
  const prompts = new Set<CaptureLivenessPrompt>([
    "neutral",
    "turn_left",
    "turn_right",
    "look_up",
    "look_down",
    "blink",
  ]);
  if (progress.prompt !== undefined && !prompts.has(progress.prompt)) {
    throw invalid("Adapter progress prompt is invalid.");
  }
  if (progress.phase === "challenge" && (!hasCount || progress.prompt === undefined)) {
    throw invalid("Challenge progress requires prompt and counts.");
  }
  if (progress.phase !== "challenge" && progress.prompt !== undefined) {
    throw invalid("Only challenge progress may include a prompt.");
  }
  if (
    progress.poseProgress !== undefined &&
    (progress.phase !== "challenge" ||
      !Number.isFinite(progress.poseProgress) ||
      progress.poseProgress < 0 ||
      progress.poseProgress > 1)
  )
    throw invalid("Pose progress is invalid.");
  const feedback = new Set([
    "find_face",
    "one_face",
    "center_face",
    "move_closer",
    "move_back",
    "face_forward",
    "follow_prompt",
    "hold_still",
    "open_eyes",
    "quality",
  ]);
  if (
    progress.poseFeedback !== undefined &&
    (progress.phase !== "challenge" || !feedback.has(progress.poseFeedback))
  )
    throw invalid("Pose feedback is invalid.");
  return {
    phase: progress.phase,
    ...(progress.current === undefined ? {} : { current: progress.current }),
    ...(progress.total === undefined ? {} : { total: progress.total }),
    ...(progress.prompt === undefined ? {} : { prompt: progress.prompt }),
    ...(progress.poseProgress === undefined ? {} : { poseProgress: progress.poseProgress }),
    ...(progress.poseFeedback === undefined ? {} : { poseFeedback: progress.poseFeedback }),
  };
}

function validateRegistryName(value: string, kind: "artefact" | "method", field: string): void {
  if (typeof value !== "string") throw invalid(`${field} is invalid.`);
  const parts = value.split(".");
  const validNamespace =
    parts.at(-2) === kind &&
    ((parts[0] === "idenqa" && parts.length === 3) || (parts[0] !== "idenqa" && parts.length >= 4));
  if (!validNamespace || parts.some((part) => !/^[a-z][a-z0-9_]*$/.test(part))) {
    throw invalid(`${field} must be a canonical namespaced ${kind} identifier.`);
  }
}

function optionalRequirementKey(value: string | undefined, field: string): void {
  if (value !== undefined && !/^[a-z][a-z0-9_]{0,63}$/.test(value))
    throw invalid(`${field} is invalid.`);
}

function optionalRegistryName(
  value: string | undefined,
  kind: "artefact" | "method",
  field: string,
): void {
  if (value !== undefined) validateRegistryName(value, kind, field);
}

function copyField(value: unknown, field: string, maximum: number): string {
  if (
    typeof value !== "string" ||
    value.trim() !== value ||
    value.length === 0 ||
    value.length > maximum
  ) {
    throw invalid(`Adapter copy ${field} is invalid.`);
  }
  return value;
}

function copyTips(value: unknown): readonly string[] {
  if (!Array.isArray(value) || value.length > 4) {
    throw invalid("Adapter copy tips must contain at most 4 items.");
  }
  return value.map((tip, index) => copyField(tip, `tips[${index}]`, 240));
}

function invalid(message: string): CaptureMethodAdapterError {
  return new CaptureMethodAdapterError("CAPTURE_METHOD_ADAPTER_INVALID", message);
}
