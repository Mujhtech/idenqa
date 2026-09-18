export const CAPTURE_ACQUISITION_SCHEMA_VERSION = "1.0" as const;

export type CaptureAcquisitionCamera = "front" | "back";
export type CaptureLivenessPrompt =
  "neutral" | "turn_left" | "turn_right" | "look_up" | "look_down" | "blink";

export interface CaptureAcquisitionQualityPolicy {
  readonly minimum_width: number;
  readonly minimum_height: number;
  readonly maximum_bytes: number;
  readonly minimum_brightness?: number;
  readonly maximum_brightness?: number;
  readonly minimum_contrast?: number;
  readonly minimum_sharpness?: number;
  readonly maximum_glare?: number;
  readonly required_face_count?: number;
}

export interface CaptureLivenessChallenge {
  readonly id: string;
  readonly prompt: CaptureLivenessPrompt;
  readonly maximum_duration_ms: number;
}

export interface CaptureAcquisitionRequirement {
  readonly id: string;
  readonly evidence_type: string;
  readonly artefact: string;
  readonly acquisition_method: "idenqa.method.live_camera";
  readonly camera: CaptureAcquisitionCamera;
  readonly quality: CaptureAcquisitionQualityPolicy;
  readonly challenges: readonly CaptureLivenessChallenge[];
}

export interface CaptureAcquisitionPlan {
  readonly schema_version: typeof CAPTURE_ACQUISITION_SCHEMA_VERSION;
  readonly plan_id: string;
  readonly session_id: string;
  readonly requirements: readonly CaptureAcquisitionRequirement[];
}

export type CaptureAcquisitionPlanErrorCode =
  | "CAPTURE_ACQUISITION_PLAN_INVALID"
  | "CAPTURE_ACQUISITION_REQUIREMENT_NOT_FOUND"
  | "CAPTURE_ACQUISITION_REQUIREMENT_AMBIGUOUS";

export class CaptureAcquisitionPlanError extends Error {
  readonly code: CaptureAcquisitionPlanErrorCode;

  constructor(code: CaptureAcquisitionPlanErrorCode, message: string) {
    super(message);
    this.name = "CaptureAcquisitionPlanError";
    this.code = code;
  }
}

const namePattern = /^[a-z0-9._-]+$/;
const referencePattern = /^[A-Za-z0-9._:-]+$/;
const verificationPattern = /^ver_[0-9A-HJKMNP-TV-Z]{26}$/;
const prompts = new Set<CaptureLivenessPrompt>([
  "neutral",
  "turn_left",
  "turn_right",
  "look_up",
  "look_down",
  "blink",
]);

export function parseCaptureAcquisitionPlan(value: unknown): CaptureAcquisitionPlan {
  const input = record(value, "capture acquisition plan");
  exactKeys(input, ["schema_version", "plan_id", "session_id", "requirements"], "plan");
  if (input.schema_version !== CAPTURE_ACQUISITION_SCHEMA_VERSION) {
    throw invalid("schema_version must be 1.0.");
  }
  const planID = boundedString(input.plan_id, "plan_id", 160, referencePattern);
  const sessionID = boundedString(input.session_id, "session_id", 30, verificationPattern);
  const values = array(input.requirements, "requirements", 1, 16);
  const requirements = values.map((item, index) => parseRequirement(item, index));
  const identifiers = new Set<string>();
  const bindings = new Set<string>();
  for (const requirement of requirements) {
    if (identifiers.has(requirement.id)) {
      throw invalid(`requirements repeats id ${JSON.stringify(requirement.id)}.`);
    }
    identifiers.add(requirement.id);
    const binding = JSON.stringify([
      requirement.evidence_type,
      requirement.artefact,
      requirement.acquisition_method,
    ]);
    if (bindings.has(binding)) {
      throw invalid("requirements contains an ambiguous evidence, artefact, and method binding.");
    }
    bindings.add(binding);
  }
  return {
    schema_version: CAPTURE_ACQUISITION_SCHEMA_VERSION,
    plan_id: planID,
    session_id: sessionID,
    requirements,
  };
}

export function findCaptureAcquisitionRequirement(
  plan: CaptureAcquisitionPlan,
  binding: {
    readonly evidenceType: string;
    readonly artefact: string;
    readonly acquisitionMethod: string;
  },
): CaptureAcquisitionRequirement {
  const matches = plan.requirements.filter(
    (requirement) =>
      requirement.evidence_type === binding.evidenceType &&
      requirement.artefact === binding.artefact &&
      requirement.acquisition_method === binding.acquisitionMethod,
  );
  if (matches.length === 0) {
    throw new CaptureAcquisitionPlanError(
      "CAPTURE_ACQUISITION_REQUIREMENT_NOT_FOUND",
      "The acquisition plan does not contain the requested capture binding.",
    );
  }
  if (matches.length !== 1) {
    throw new CaptureAcquisitionPlanError(
      "CAPTURE_ACQUISITION_REQUIREMENT_AMBIGUOUS",
      "The acquisition plan contains more than one matching capture binding.",
    );
  }
  return matches[0]!;
}

function parseRequirement(value: unknown, index: number): CaptureAcquisitionRequirement {
  const input = record(value, `requirements[${index}]`);
  exactKeys(
    input,
    ["id", "evidence_type", "artefact", "acquisition_method", "camera", "quality", "challenges"],
    `requirements[${index}]`,
  );
  const method = input.acquisition_method;
  if (method !== "idenqa.method.live_camera") {
    throw invalid(`requirements[${index}].acquisition_method must be idenqa.method.live_camera.`);
  }
  const camera = input.camera;
  if (camera !== "front" && camera !== "back") {
    throw invalid(`requirements[${index}].camera must be front or back.`);
  }
  const challenges = array(input.challenges, `requirements[${index}].challenges`, 0, 8).map(
    (item, challengeIndex) => parseChallenge(item, index, challengeIndex),
  );
  const challengeIDs = new Set<string>();
  for (const challenge of challenges) {
    if (challengeIDs.has(challenge.id)) {
      throw invalid(
        `requirements[${index}].challenges repeats id ${JSON.stringify(challenge.id)}.`,
      );
    }
    challengeIDs.add(challenge.id);
  }
  return {
    id: boundedString(input.id, `requirements[${index}].id`, 160, referencePattern),
    evidence_type: boundedString(
      input.evidence_type,
      `requirements[${index}].evidence_type`,
      160,
      namePattern,
    ),
    artefact: boundedString(input.artefact, `requirements[${index}].artefact`, 160, namePattern),
    acquisition_method: method,
    camera,
    quality: parseQuality(input.quality, index),
    challenges,
  };
}

function parseQuality(value: unknown, requirementIndex: number): CaptureAcquisitionQualityPolicy {
  const field = `requirements[${requirementIndex}].quality`;
  const input = record(value, field);
  exactKeys(
    input,
    [
      "minimum_width",
      "minimum_height",
      "maximum_bytes",
      "minimum_brightness",
      "maximum_brightness",
      "minimum_contrast",
      "minimum_sharpness",
      "maximum_glare",
      "required_face_count",
    ],
    field,
    ["minimum_width", "minimum_height", "maximum_bytes"],
  );
  const minimumBrightness = optionalNumber(input.minimum_brightness, `${field}.minimum_brightness`);
  const maximumBrightness = optionalNumber(input.maximum_brightness, `${field}.maximum_brightness`);
  if (
    minimumBrightness !== undefined &&
    maximumBrightness !== undefined &&
    minimumBrightness > maximumBrightness
  ) {
    throw invalid(`${field}.minimum_brightness must not exceed maximum_brightness.`);
  }
  return {
    minimum_width: integer(input.minimum_width, `${field}.minimum_width`, 320, 16_384),
    minimum_height: integer(input.minimum_height, `${field}.minimum_height`, 320, 16_384),
    maximum_bytes: integer(input.maximum_bytes, `${field}.maximum_bytes`, 1_024, 20_971_520),
    ...(minimumBrightness === undefined ? {} : { minimum_brightness: minimumBrightness }),
    ...(maximumBrightness === undefined ? {} : { maximum_brightness: maximumBrightness }),
    ...optionalUnitField(input, "minimum_contrast", field),
    ...optionalUnitField(input, "minimum_sharpness", field),
    ...optionalUnitField(input, "maximum_glare", field),
    ...(input.required_face_count === undefined
      ? {}
      : {
          required_face_count: integer(
            input.required_face_count,
            `${field}.required_face_count`,
            0,
            4,
          ),
        }),
  };
}

function parseChallenge(
  value: unknown,
  requirementIndex: number,
  challengeIndex: number,
): CaptureLivenessChallenge {
  const field = `requirements[${requirementIndex}].challenges[${challengeIndex}]`;
  const input = record(value, field);
  exactKeys(input, ["id", "prompt", "maximum_duration_ms"], field);
  if (typeof input.prompt !== "string" || !prompts.has(input.prompt as CaptureLivenessPrompt)) {
    throw invalid(`${field}.prompt is not supported.`);
  }
  return {
    id: boundedString(input.id, `${field}.id`, 160, referencePattern),
    prompt: input.prompt as CaptureLivenessPrompt,
    maximum_duration_ms: integer(
      input.maximum_duration_ms,
      `${field}.maximum_duration_ms`,
      500,
      15_000,
    ),
  };
}

function record(value: unknown, field: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw invalid(`${field} must be an object.`);
  }
  return value as Record<string, unknown>;
}

function exactKeys(
  value: Record<string, unknown>,
  allowed: readonly string[],
  field: string,
  required: readonly string[] = allowed,
): void {
  const allowedSet = new Set(allowed);
  for (const key of Object.keys(value)) {
    if (!allowedSet.has(key))
      throw invalid(`${field} contains unknown field ${JSON.stringify(key)}.`);
  }
  for (const key of required) {
    if (!(key in value)) throw invalid(`${field} is missing ${key}.`);
  }
}

function array(
  value: unknown,
  field: string,
  minimum: number,
  maximum: number,
): readonly unknown[] {
  if (!Array.isArray(value) || value.length < minimum || value.length > maximum) {
    throw invalid(`${field} must contain between ${minimum} and ${maximum} items.`);
  }
  return value;
}

function boundedString(value: unknown, field: string, maximum: number, pattern: RegExp): string {
  if (
    typeof value !== "string" ||
    value.length === 0 ||
    value.length > maximum ||
    !pattern.test(value)
  ) {
    throw invalid(`${field} is invalid.`);
  }
  return value;
}

function integer(value: unknown, field: string, minimum: number, maximum: number): number {
  if (!Number.isSafeInteger(value) || (value as number) < minimum || (value as number) > maximum) {
    throw invalid(`${field} must be an integer from ${minimum} through ${maximum}.`);
  }
  return value as number;
}

function optionalNumber(value: unknown, field: string): number | undefined {
  if (value === undefined) return undefined;
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0 || value > 1) {
    throw invalid(`${field} must be a number from 0 through 1.`);
  }
  return value;
}

function optionalUnitField(
  input: Record<string, unknown>,
  key: "minimum_contrast" | "minimum_sharpness" | "maximum_glare",
  field: string,
): Partial<CaptureAcquisitionQualityPolicy> {
  const value = optionalNumber(input[key], `${field}.${key}`);
  return value === undefined ? {} : { [key]: value };
}

function invalid(message: string): CaptureAcquisitionPlanError {
  return new CaptureAcquisitionPlanError("CAPTURE_ACQUISITION_PLAN_INVALID", message);
}
