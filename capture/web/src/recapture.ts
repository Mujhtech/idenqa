import type { IdenqaCaptureElement, CaptureElementStartOptions } from "./capture-element.js";
import type { CaptureFlowSnapshot } from "./flow.js";

export interface RecaptureHandoff {
  readonly verificationId: string;
  readonly captureToken: string;
  readonly outcomeToken: string;
  readonly outcomeExpiresAt: string;
  readonly expiresAt: string;
}
/** The tenant backend authenticates the subject and returns only a linked capture credential. */
export function createRecaptureHandoff(
  element: IdenqaCaptureElement,
  options: Omit<
    CaptureElementStartOptions,
    "captureToken" | "outcomeToken" | "expectedVerificationId"
  >,
  obtain: (request: {
    readonly recoverVerificationId?: string;
    readonly signal: AbortSignal;
  }) => Promise<RecaptureHandoff>,
): {
  start(): Promise<CaptureFlowSnapshot>;
  recover(): Promise<CaptureFlowSnapshot>;
  cancel(): void;
} {
  let child: string | undefined;
  let pending: AbortController | undefined;
  const cancel = () => {
    pending?.abort();
    pending = undefined;
    element.cancel();
  };
  const run = async (recover: boolean) => {
    if (recover && child === undefined) throw new Error("Start a recapture before recovering it.");
    cancel();
    const controller = new AbortController();
    pending = controller;
    const handoff = await obtain({
      signal: controller.signal,
      ...(recover && child !== undefined ? { recoverVerificationId: child } : {}),
    });
    if (controller.signal.aborted || pending !== controller)
      throw new Error("Recapture handoff cancelled.");
    if (
      !handoff.verificationId ||
      !handoff.captureToken ||
      !handoff.outcomeToken ||
      !Number.isFinite(Date.parse(handoff.expiresAt)) ||
      !Number.isFinite(Date.parse(handoff.outcomeExpiresAt)) ||
      Date.parse(handoff.expiresAt) <= Date.now() ||
      Date.parse(handoff.outcomeExpiresAt) <= Date.now() ||
      (recover && handoff.verificationId !== child)
    )
      throw new Error("Invalid recapture credential.");
    child = handoff.verificationId;
    const snapshot = await element.start({
      ...options,
      captureToken: handoff.captureToken,
      outcomeToken: handoff.outcomeToken,
      expectedVerificationId: child,
    });
    if (controller.signal.aborted || pending !== controller)
      throw new Error("Recapture handoff cancelled.");
    return snapshot;
  };
  return { start: () => run(false), recover: () => run(true), cancel };
}
