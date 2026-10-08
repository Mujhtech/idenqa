import type { CapturePlan, CapturePlanStep } from "../planner.js";
import type { StepCompletion } from "./state.js";
import type { CaptureProgressDetail } from "./types.js";

export function stepKey(step: CapturePlanStep): string {
  return JSON.stringify([
    step.requirementKey,
    step.artefact,
    step.methodOptions,
    step.fallbackCondition,
  ]);
}

export function captureProgress(
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

export function planSteps(plan: CapturePlan): readonly CapturePlanStep[] {
  return plan.requirements.flatMap((requirement) => requirement.steps);
}
