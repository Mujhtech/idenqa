import type { CaptureRequirement, EvidenceMediaType, VerificationSession } from "@idenqa/sdk";

import type { CapturePlanStep } from "./planner.js";

export const FILE_UPLOAD_METHOD = "idenqa.method.file_upload";
export const FILE_UPLOAD_HARD_MAXIMUM_BYTES = 64 * 1024 * 1024;

const allowedMediaConstraint = "idenqa.constraint.allowed_media_types";
const maximumBytesConstraint = "idenqa.constraint.maximum_bytes";
const supportedMediaTypes = ["image/jpeg", "image/png"] as const;

export interface FileUploadPolicy {
  readonly allowedMediaTypes: readonly EvidenceMediaType[];
  readonly maximumBytes: number;
  readonly hasExplicitMaximum: boolean;
}

export class CaptureUploadError extends Error {
  readonly code:
    "CAPTURE_UPLOAD_INVALID_FILE" | "CAPTURE_UPLOAD_INVALID_STATE" | "CAPTURE_UPLOAD_RETRY_LATER";

  constructor(code: CaptureUploadError["code"], message: string) {
    super(message);
    this.name = "CaptureUploadError";
    this.code = code;
  }
}

export function fileUploadPolicy(
  session: VerificationSession,
  step: CapturePlanStep,
): FileUploadPolicy {
  const requirement = session.requirements.requirements.find(
    (item) => item.key === step.requirementKey,
  );
  if (requirement === undefined || requirement.evidence_type !== step.evidenceType) {
    throw invalidFile("The upload step does not match the capture requirements.");
  }
  return policyFromRequirement(requirement);
}

export async function prepareFileUpload(
  body: Blob,
  policy: FileUploadPolicy,
): Promise<{
  readonly body: Blob;
  readonly digest: string;
  readonly mediaType: EvidenceMediaType;
}> {
  if (!(body instanceof Blob) || body.size <= 0) {
    throw invalidFile("Choose one non-empty JPEG or PNG file.");
  }
  if (!isEvidenceMediaType(body.type) || !policy.allowedMediaTypes.includes(body.type)) {
    throw invalidFile("Choose a JPEG or PNG file allowed by this verification.");
  }
  if (body.size > policy.maximumBytes || body.size > FILE_UPLOAD_HARD_MAXIMUM_BYTES) {
    throw invalidFile(`Choose a file no larger than ${formatBytes(policy.maximumBytes)}.`);
  }
  await verifySignature(body, body.type);
  const bytes = await body.arrayBuffer();
  const digest = await globalThis.crypto.subtle.digest("SHA-256", bytes);
  return { body, digest: `sha256:${hex(new Uint8Array(digest))}`, mediaType: body.type };
}

export function formatBytes(bytes: number): string {
  const mebibyte = 1024 * 1024;
  if (bytes % mebibyte === 0) return `${bytes / mebibyte} MiB`;
  return `${Math.ceil(bytes / 1024)} KiB`;
}

function policyFromRequirement(requirement: CaptureRequirement): FileUploadPolicy {
  let allowedMediaTypes: readonly EvidenceMediaType[] = supportedMediaTypes;
  let maximumBytes = FILE_UPLOAD_HARD_MAXIMUM_BYTES;
  let hasExplicitMaximum = false;
  for (const constraint of requirement.constraints) {
    if (constraint.name === allowedMediaConstraint) {
      if (!Array.isArray(constraint.value) || constraint.value.length === 0) {
        throw invalidFile("The allowed media-type constraint is invalid.");
      }
      const values = constraint.value.filter(isEvidenceMediaType);
      if (values.length !== constraint.value.length || new Set(values).size !== values.length) {
        throw invalidFile("The allowed media-type constraint is unsupported.");
      }
      allowedMediaTypes = values;
    }
    if (constraint.name === maximumBytesConstraint) {
      if (
        typeof constraint.value !== "number" ||
        !Number.isSafeInteger(constraint.value) ||
        constraint.value <= 0 ||
        constraint.value > FILE_UPLOAD_HARD_MAXIMUM_BYTES
      ) {
        throw invalidFile("The maximum upload-size constraint is invalid.");
      }
      maximumBytes = constraint.value;
      hasExplicitMaximum = true;
    }
  }
  return { allowedMediaTypes, maximumBytes, hasExplicitMaximum };
}

async function verifySignature(body: Blob, mediaType: EvidenceMediaType): Promise<void> {
  const prefix = new Uint8Array(await body.slice(0, 8).arrayBuffer());
  const valid =
    mediaType === "image/png"
      ? matches(prefix, [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
      : matches(prefix, [0xff, 0xd8, 0xff]);
  if (!valid) throw invalidFile("The selected file content does not match its JPEG or PNG type.");
}

function matches(value: Uint8Array, expected: readonly number[]): boolean {
  return value.length >= expected.length && expected.every((byte, index) => value[index] === byte);
}

function isEvidenceMediaType(value: unknown): value is EvidenceMediaType {
  return value === "image/jpeg" || value === "image/png";
}

function hex(value: Uint8Array): string {
  return [...value].map((byte) => byte.toString(16).padStart(2, "0")).join("");
}

function invalidFile(message: string): CaptureUploadError {
  return new CaptureUploadError("CAPTURE_UPLOAD_INVALID_FILE", message);
}
