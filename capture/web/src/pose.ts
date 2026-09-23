import type { CaptureLivenessChallenge } from "./acquisition.js";

/** Local measurements only. Angles are degrees: positive yaw is the subject's
 * right, positive pitch is up. Measurements never establish liveness assurance. */
export interface CaptureFacePose {
  readonly faceCount: number;
  readonly yaw: number;
  readonly pitch: number;
  readonly roll: number;
  readonly centerX: number;
  readonly centerY: number;
  readonly width: number;
  readonly height: number;
  readonly leftEyeClosed: number;
  readonly rightEyeClosed: number;
}

export interface CapturePosePolicy {
  readonly target_degrees: number;
  readonly tolerance_degrees: number;
  readonly hold_duration_ms: number;
}

export const CAPTURE_POSE_DEFAULTS: CapturePosePolicy = Object.freeze({
  target_degrees: 20,
  tolerance_degrees: 7,
  hold_duration_ms: 450,
});

export type CapturePoseFeedback =
  | "find_face"
  | "one_face"
  | "center_face"
  | "move_closer"
  | "move_back"
  | "face_forward"
  | "follow_prompt"
  | "hold_still"
  | "open_eyes"
  | "quality";

export interface CapturePoseProgress {
  readonly fraction: number;
  readonly feedback: CapturePoseFeedback;
  readonly complete: boolean;
}

/** One challenge's contiguous, measured hold. Every challenge starts from a
 * stable neutral pose; old pose/hold samples never carry into the next one. */
export class CapturePoseGate {
  readonly #prompt: CaptureLivenessChallenge["prompt"];
  readonly #policy: CapturePosePolicy;
  #baseline: CaptureFacePose | undefined;
  #since: number | undefined;
  #last = -Infinity;
  #samples = 0;
  #blinkClosed = false;
  #blinkSamples = 0;

  constructor(challenge: CaptureLivenessChallenge) {
    this.#prompt = challenge.prompt;
    this.#policy = challenge.pose ?? CAPTURE_POSE_DEFAULTS;
  }

  reset(): void {
    this.#since = undefined;
    this.#samples = 0;
    this.#blinkClosed = false;
    this.#blinkSamples = 0;
  }

  update(pose: CaptureFacePose, at: number, quality = true): CapturePoseProgress {
    const reject = (feedback: CapturePoseFeedback): CapturePoseProgress => {
      this.reset();
      return { fraction: 0, feedback, complete: false };
    };
    if (!Number.isFinite(at) || at <= this.#last) return reject("find_face");
    if (at - this.#last > 500) this.reset();
    this.#last = at;
    if (
      !pose ||
      ![
        pose.faceCount,
        pose.yaw,
        pose.pitch,
        pose.roll,
        pose.centerX,
        pose.centerY,
        pose.width,
        pose.height,
        pose.leftEyeClosed,
        pose.rightEyeClosed,
      ].every(Number.isFinite) ||
      pose.faceCount < 1 ||
      [
        pose.centerX,
        pose.centerY,
        pose.width,
        pose.height,
        pose.leftEyeClosed,
        pose.rightEyeClosed,
      ].some((value) => value < 0 || value > 1)
    ) {
      this.#baseline = undefined;
      return reject("find_face");
    }
    if (pose.faceCount !== 1) {
      this.#baseline = undefined;
      return reject("one_face");
    }
    if (!quality) return reject("quality");
    if (pose.width < 0.22 || pose.height < 0.28) return reject("move_closer");
    if (pose.width > 0.8 || pose.height > 0.88) return reject("move_back");
    if (
      Math.abs(pose.centerX - 0.5) > 0.18 ||
      Math.abs(pose.centerY - 0.5) > 0.2 ||
      Math.abs(pose.roll) > 12
    )
      return reject("center_face");
    const neutral = Math.abs(pose.yaw) <= 10 && Math.abs(pose.pitch) <= 12;
    if (this.#baseline === undefined) {
      if (!neutral || pose.leftEyeClosed > 0.35 || pose.rightEyeClosed > 0.35)
        return reject("face_forward");
      const held = this.#hold(at, this.#policy.hold_duration_ms);
      if (this.#prompt === "neutral")
        return { fraction: held, feedback: "hold_still", complete: held === 1 };
      if (held === 1) {
        this.#baseline = pose;
        this.reset();
      }
      return { fraction: 0, feedback: "face_forward", complete: false };
    }
    const yaw = pose.yaw - this.#baseline.yaw;
    const pitch = pose.pitch - this.#baseline.pitch;
    if (this.#prompt === "blink") {
      if (!neutral) return reject("face_forward");
      if (pose.leftEyeClosed >= 0.65 && pose.rightEyeClosed >= 0.65) {
        this.#blinkSamples++;
        this.#blinkClosed ||= this.#blinkSamples >= 2;
        this.#since = undefined;
        this.#samples = 0;
        return { fraction: this.#blinkClosed ? 0.6 : 0.3, feedback: "open_eyes", complete: false };
      }
      if (!this.#blinkClosed || pose.leftEyeClosed > 0.35 || pose.rightEyeClosed > 0.35)
        return reject("follow_prompt");
      const held = this.#hold(at, this.#policy.hold_duration_ms);
      return { fraction: 0.6 + 0.4 * held, feedback: "hold_still", complete: held === 1 };
    }
    const angle =
      this.#prompt === "turn_right"
        ? yaw
        : this.#prompt === "turn_left"
          ? -yaw
          : this.#prompt === "look_up"
            ? pitch
            : -pitch;
    const crossAxis = this.#prompt.startsWith("turn_") ? pitch : yaw;
    if (Math.abs(crossAxis) > 12 || pose.leftEyeClosed > 0.5 || pose.rightEyeClosed > 0.5)
      return reject("follow_prompt");
    const target = this.#policy.target_degrees;
    const tolerance = this.#policy.tolerance_degrees;
    if (Math.abs(angle - target) > tolerance) {
      this.reset();
      return {
        fraction:
          angle > target + tolerance ? 0 : Math.max(0, Math.min(0.7, (angle / target) * 0.7)),
        feedback: "follow_prompt",
        complete: false,
      };
    }
    const held = this.#hold(at, this.#policy.hold_duration_ms);
    return { fraction: 0.7 + 0.3 * held, feedback: "hold_still", complete: held === 1 };
  }

  #hold(at: number, duration: number): number {
    this.#since ??= at;
    this.#samples++;
    return Math.min((at - this.#since) / duration, this.#samples / 4, 1);
  }
}
