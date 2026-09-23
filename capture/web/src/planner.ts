import type {
  CaptureAcquisition,
  CaptureDocumentOption,
  CaptureFallback,
  CaptureFallbackReason,
  CaptureRequirement,
  VerificationSession,
} from "@idenqa/sdk";

export interface CapturePlannerCapabilities {
  /** Canonical acquisition methods implemented by this package and host integration. */
  readonly supportedMethods: readonly string[];
  /** Supported methods currently usable by this device and browsing context. */
  readonly availableMethods: readonly string[];
}

export interface CapturePlanStep {
  readonly requirementKey: string;
  readonly evidenceType: string;
  readonly artefact: string;
  /** One choice for `all_of`; one or more subject choices for `any_of`. */
  readonly methodOptions: readonly string[];
  readonly fallbackCondition?: CaptureRuntimeFallbackReason;
}

export type CapturePlanFallbackReason = Exclude<CaptureFallbackReason, "capture_failed">;
export type CaptureRuntimeFallbackReason = CaptureFallbackReason;

export interface CapturePlanRequirement {
  readonly key: string;
  readonly purpose: string;
  readonly evidenceType: string;
  readonly requiredAssurances: readonly string[];
  readonly steps: readonly CapturePlanStep[];
  readonly documentOptions?: readonly CaptureDocumentOption[];
  readonly selectedDocument?: string;
}

export interface CapturePlan {
  readonly verificationId: string;
  readonly profileRevision: number;
  readonly profileDigest: string;
  readonly requirements: readonly CapturePlanRequirement[];
}

export type CapturePlanErrorCode =
  "CAPTURE_PLAN_INVALID_SESSION" | "CAPTURE_PLAN_NO_COMPATIBLE_METHOD";

export class CapturePlanError extends Error {
  readonly code: CapturePlanErrorCode;
  readonly requirementKey?: string;

  constructor(code: CapturePlanErrorCode, message: string, requirementKey?: string) {
    super(message);
    this.name = "CapturePlanError";
    this.code = code;
    if (requirementKey !== undefined) this.requirementKey = requirementKey;
  }
}

export function createCapturePlan(
  session: VerificationSession,
  capabilities: CapturePlannerCapabilities,
): CapturePlan {
  const methods = methodCapabilities(capabilities);
  const requirementKeys = new Set<string>();
  if (session.requirements.requirements.length === 0) {
    throw invalidSession("The session contains no capture requirements.");
  }

  const requirements = session.requirements.requirements.map((requirement) => {
    const key = canonicalValue(requirement.key, "requirement key");
    if (requirementKeys.has(key)) {
      throw invalidSession("The session contains duplicate requirement keys.");
    }
    requirementKeys.add(key);

    if (requirement.document_options !== undefined) {
      const options = requirement.document_options;
      if (
        requirement.evidence_type !== "idenqa.evidence.document_image" ||
        options.length === 0 ||
        options.length > 16 ||
        new Set(options.map((option) => option.id)).size !== options.length ||
        options.some(
          (option) =>
            !option.id.trim() ||
            !option.label.trim() ||
            option.artefacts.length === 0 ||
            option.artefacts.some((artefact) => !requirement.artefacts.includes(artefact)),
        )
      ) {
        throw invalidSession("The pinned document choices are invalid.");
      }
    }
    const acquisition = selectAcquisition(requirement, methods);
    const selectedDocument = session.documentSelections?.[key];
    const option = requirement.document_options?.find(
      (candidate) => candidate.id === selectedDocument,
    );
    if (selectedDocument !== undefined && option === undefined) {
      throw invalidSession("The selected document is not in the pinned capture requirement.");
    }
    return {
      key,
      purpose: canonicalValue(requirement.purpose, "requirement purpose"),
      evidenceType: canonicalValue(requirement.evidence_type, "evidence type"),
      requiredAssurances: canonicalUniqueValues(
        requirement.required_assurances,
        "required assurances",
      ),
      steps: createSteps(
        option === undefined ? requirement : { ...requirement, artefacts: option.artefacts },
        acquisition,
      ),
      ...(requirement.document_options === undefined
        ? {}
        : { documentOptions: requirement.document_options }),
      ...(selectedDocument === undefined ? {} : { selectedDocument }),
    } satisfies CapturePlanRequirement;
  });

  return {
    verificationId: canonicalValue(session.id, "verification ID"),
    profileRevision: positiveInteger(session.profileRevision, "profile revision"),
    profileDigest: canonicalValue(session.profileDigest, "profile digest"),
    requirements,
  };
}

export function applyCaptureFailureFallback(
  plan: CapturePlan,
  session: VerificationSession,
  failedStep: CapturePlanStep,
  capabilities: CapturePlannerCapabilities,
): CapturePlan {
  const requirement = session.requirements.requirements.find(
    (item) => item.key === failedStep.requirementKey,
  );
  const plannedRequirement = plan.requirements.find(
    (item) => item.key === failedStep.requirementKey,
  );
  if (
    requirement === undefined ||
    plannedRequirement === undefined ||
    requirement.evidence_type !== failedStep.evidenceType
  ) {
    throw invalidSession("The failed capture step does not match the session requirements.");
  }
  const failedIndex = plannedRequirement.steps.findIndex((step) => sameStep(step, failedStep));
  if (failedIndex < 0) {
    throw invalidSession("The failed capture step is not present in the current plan.");
  }
  const methods = methodCapabilities(capabilities);
  let replacement: readonly CapturePlanStep[] | undefined;
  for (const fallback of requirement.fallbacks) {
    if (!fallback.on.includes("capture_failed")) continue;
    const selected = evaluateAcquisition(fallback.acquisition, methods).selected;
    if (selected === undefined) continue;
    replacement = createStepsForArtefact(requirement, failedStep.artefact, {
      ...selected,
      fallbackCondition: "capture_failed",
    });
    break;
  }
  if (replacement === undefined) {
    throw new CapturePlanError(
      "CAPTURE_PLAN_NO_COMPATIBLE_METHOD",
      "No policy-approved fallback is available after the capture attempt failed.",
      requirement.key,
    );
  }
  return {
    ...plan,
    requirements: plan.requirements.map((item) =>
      item.key === plannedRequirement.key
        ? {
            ...item,
            steps: item.steps.flatMap((step, index) =>
              index === failedIndex ? replacement : [step],
            ),
          }
        : item,
    ),
  };
}

interface SelectedAcquisition {
  readonly acquisition: CaptureAcquisition;
  readonly availableMethods: readonly string[];
  readonly fallbackCondition?: CaptureRuntimeFallbackReason;
}

interface MethodCapabilities {
  readonly supportedMethods: readonly string[];
  readonly availableMethods: readonly string[];
}

interface AcquisitionEvaluation {
  readonly selected?: SelectedAcquisition;
  readonly unavailableReasons: readonly CapturePlanFallbackReason[];
}

function selectAcquisition(
  requirement: CaptureRequirement,
  capabilities: MethodCapabilities,
): SelectedAcquisition {
  const primary = evaluateAcquisition(requirement.acquisition, capabilities);
  if (primary.selected !== undefined) return primary.selected;

  for (const fallback of requirement.fallbacks) {
    const condition = fallbackCondition(fallback, primary.unavailableReasons);
    if (condition === undefined) continue;
    const selected = evaluateAcquisition(fallback.acquisition, capabilities).selected;
    if (selected !== undefined) return { ...selected, fallbackCondition: condition };
  }

  throw new CapturePlanError(
    "CAPTURE_PLAN_NO_COMPATIBLE_METHOD",
    "No policy-approved acquisition method is available for this requirement.",
    requirement.key,
  );
}

function methodCapabilities(capabilities: CapturePlannerCapabilities): MethodCapabilities {
  const supportedMethods = uniqueCanonicalValues(capabilities.supportedMethods, "supportedMethods");
  const availableMethods = canonicalUniqueValues(capabilities.availableMethods, "availableMethods");
  if (availableMethods.some((method) => !supportedMethods.includes(method))) {
    throw invalidSession("availableMethods must be a subset of supportedMethods.");
  }
  return { supportedMethods, availableMethods };
}

function evaluateAcquisition(
  acquisition: CaptureAcquisition,
  capabilities: MethodCapabilities,
): AcquisitionEvaluation {
  const methods = uniqueCanonicalValues(acquisition.methods, "acquisition methods");
  const supported = methods.filter((method) => capabilities.supportedMethods.includes(method));
  const available = methods.filter((method) => capabilities.availableMethods.includes(method));
  if (acquisition.strategy === "any_of" && available.length > 0) {
    return { selected: { acquisition, availableMethods: available }, unavailableReasons: [] };
  }
  if (acquisition.strategy === "all_of" && available.length === methods.length) {
    return { selected: { acquisition, availableMethods: available }, unavailableReasons: [] };
  }
  if (acquisition.strategy !== "any_of" && acquisition.strategy !== "all_of") {
    throw invalidSession("The session contains an unsupported acquisition strategy.");
  }
  const unavailableReasons: CapturePlanFallbackReason[] = [];
  if (supported.length !== methods.length) unavailableReasons.push("method_unavailable");
  if (supported.some((method) => !capabilities.availableMethods.includes(method))) {
    unavailableReasons.push("capability_unavailable");
  }
  return { unavailableReasons };
}

function fallbackCondition(
  fallback: CaptureFallback,
  unavailableReasons: readonly CapturePlanFallbackReason[],
): CapturePlanFallbackReason | undefined {
  for (const condition of fallback.on) {
    if (condition !== "capture_failed" && unavailableReasons.includes(condition)) return condition;
  }
  return undefined;
}

function createSteps(
  requirement: CaptureRequirement,
  selected: SelectedAcquisition,
): readonly CapturePlanStep[] {
  const artefacts = [...uniqueCanonicalValues(requirement.artefacts, "requirement artefacts")];
  // Core canonicalises the artefact set lexically. Present the familiar front
  // then back journey without removing requirements or changing their bindings.
  const front = artefacts.indexOf("idenqa.artefact.document_front");
  const back = artefacts.indexOf("idenqa.artefact.document_back");
  if (front >= 0 && back >= 0 && back < front) {
    [artefacts[back], artefacts[front]] = [artefacts[front]!, artefacts[back]!];
  }
  if (selected.acquisition.strategy === "any_of") {
    return artefacts.map((artefact) =>
      step(requirement, artefact, selected.availableMethods, selected),
    );
  }
  return artefacts.flatMap((artefact) =>
    selected.availableMethods.map((method) => step(requirement, artefact, [method], selected)),
  );
}

function createStepsForArtefact(
  requirement: CaptureRequirement,
  artefact: string,
  selected: SelectedAcquisition,
): readonly CapturePlanStep[] {
  if (!requirement.artefacts.includes(artefact)) {
    throw invalidSession("The capture artefact is not present in its requirement.");
  }
  if (selected.acquisition.strategy === "any_of") {
    return [step(requirement, artefact, selected.availableMethods, selected)];
  }
  return selected.availableMethods.map((method) => step(requirement, artefact, [method], selected));
}

function step(
  requirement: CaptureRequirement,
  artefact: string,
  methodOptions: readonly string[],
  selected: SelectedAcquisition,
): CapturePlanStep {
  return {
    requirementKey: requirement.key,
    evidenceType: requirement.evidence_type,
    artefact,
    methodOptions,
    ...(selected.fallbackCondition === undefined
      ? {}
      : { fallbackCondition: selected.fallbackCondition }),
  };
}

function sameStep(first: CapturePlanStep, second: CapturePlanStep): boolean {
  return (
    first.requirementKey === second.requirementKey &&
    first.evidenceType === second.evidenceType &&
    first.artefact === second.artefact &&
    first.fallbackCondition === second.fallbackCondition &&
    first.methodOptions.length === second.methodOptions.length &&
    first.methodOptions.every((method, index) => method === second.methodOptions[index])
  );
}

function uniqueCanonicalValues(values: readonly string[], field: string): readonly string[] {
  if (values.length === 0) throw invalidSession(`${field} must not be empty.`);
  return canonicalUniqueValues(values, field);
}

function canonicalUniqueValues(values: readonly string[], field: string): readonly string[] {
  const result = values.map((value) => canonicalValue(value, field));
  if (new Set(result).size !== result.length) {
    throw invalidSession(`${field} must not contain duplicates.`);
  }
  return result;
}

function canonicalValue(value: string, field: string): string {
  if (value.length === 0 || value.trim() !== value) {
    throw invalidSession(`${field} must be a non-empty canonical value.`);
  }
  return value;
}

function positiveInteger(value: number, field: string): number {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw invalidSession(`${field} must be a positive integer.`);
  }
  return value;
}

function invalidSession(message: string): CapturePlanError {
  return new CapturePlanError("CAPTURE_PLAN_INVALID_SESSION", message);
}
