import { describe, expect, it } from "vitest";
import { CapturePoseGate, type CaptureFacePose } from "../src/pose.js";
import type { CaptureLivenessPrompt } from "../src/acquisition.js";

const neutral: CaptureFacePose = {
  faceCount: 1,
  yaw: 0,
  pitch: 0,
  roll: 0,
  centerX: 0.5,
  centerY: 0.5,
  width: 0.4,
  height: 0.6,
  leftEyeClosed: 0,
  rightEyeClosed: 0,
};
const gate = (prompt: CaptureLivenessPrompt) =>
  new CapturePoseGate({ id: "test", prompt, maximum_duration_ms: 5000 });
function calibrate(target: CapturePoseGate) {
  for (let i = 0; i <= 500; i += 100) target.update(neutral, i);
}

describe("measured pose gate", () => {
  it("confirms centering only after a measured neutral hold, then exposes the pose stage", () => {
    const target = gate("turn_left");
    expect(target.update(neutral, 0).stage).toBe("centering");
    for (let t = 100; t < 500; t += 100) {
      expect(target.update(neutral, t).stage).toBe("centering");
    }
    expect(target.update(neutral, 500)).toMatchObject({ stage: "centered", complete: false });
    expect(target.update(neutral, 600)).toMatchObject({ stage: "pose", complete: false });
    expect(target.update({ ...neutral, faceCount: 0 }, 700).stage).toBe("centering");
    expect(target.update(neutral, 800).stage).toBe("centering");
  });

  it("never shows centering confirmation for an off-center or multiple face sample", () => {
    const target = gate("neutral");
    for (let t = 0; t <= 2000; t += 100) {
      expect(target.update({ ...neutral, centerX: 0.9 }, t)).toMatchObject({
        stage: "centering",
        complete: false,
      });
    }
    expect(target.update({ ...neutral, faceCount: 2 }, 2100).stage).toBe("centering");
  });

  it.each(["turn_left", "turn_right", "look_up", "look_down"] as const)(
    "does not advance %s while stationary or moving the wrong way",
    (prompt) => {
      const target = gate(prompt);
      calibrate(target);
      for (let t = 600; t <= 3000; t += 100) expect(target.update(neutral, t).complete).toBe(false);
      const wrong = {
        ...neutral,
        yaw: prompt === "turn_left" ? 20 : prompt === "turn_right" ? -20 : 0,
        pitch: prompt === "look_up" ? -20 : prompt === "look_down" ? 20 : 0,
      };
      for (let t = 3100; t <= 4500; t += 100) expect(target.update(wrong, t).fraction).toBe(0);
    },
  );
  it.each(["turn_left", "turn_right", "look_up", "look_down"] as const)(
    "requires sustained target samples for %s",
    (prompt) => {
      const target = gate(prompt);
      calibrate(target);
      const pose = {
        ...neutral,
        yaw: prompt === "turn_left" ? -20 : prompt === "turn_right" ? 20 : 0,
        pitch: prompt === "look_up" ? 20 : prompt === "look_down" ? -20 : 0,
      };
      expect(target.update(pose, 600)).toMatchObject({ complete: false, fraction: 0.7 });
      for (let t = 700; t <= 1000; t += 100) expect(target.update(pose, t).complete).toBe(false);
      expect(target.update(pose, 1100)).toMatchObject({ complete: true, fraction: 1 });
    },
  );
  it.each([
    { faceCount: 0 },
    { faceCount: 2 },
    { yaw: NaN },
    { roll: 30 },
    { width: 0.1 },
    { height: 0.95 },
    { centerX: 0.9 },
    { pitch: 25 },
  ])("resets a partially completed hold on invalid tracking: %j", (bad) => {
    const target = gate("turn_right");
    calibrate(target);
    target.update({ ...neutral, yaw: 20 }, 600);
    target.update({ ...neutral, yaw: 20 }, 900);
    expect(target.update({ ...neutral, yaw: 20, ...bad }, 1000).complete).toBe(false);
    expect(target.update({ ...neutral, yaw: 20 }, 1100).complete).toBe(false);
  });
  it("cannot use stale samples, gaps or insufficient-quality frames to finish a hold", () => {
    const target = gate("turn_right");
    calibrate(target);
    const pose = { ...neutral, yaw: 20 };
    target.update(pose, 600);
    expect(target.update(pose, 600).complete).toBe(false);
    expect(target.update(pose, 2000).complete).toBe(false);
    expect(target.update(pose, 2100, false)).toMatchObject({ fraction: 0, feedback: "quality" });
    expect(target.update(pose, 2200).complete).toBe(false);
  });
  it("requires open, closed and reopened eyes rather than accepting a closed-eye still", () => {
    const target = gate("blink");
    const closed = { ...neutral, leftEyeClosed: 0.9, rightEyeClosed: 0.9 };
    expect(target.update(closed, 0).complete).toBe(false);
    for (let i = 100; i <= 600; i += 100) target.update(neutral, i);
    target.update(closed, 700);
    target.update(closed, 800);
    for (let i = 900; i < 1400; i += 100) expect(target.update(neutral, i).complete).toBe(false);
    expect(target.update(neutral, 1400).complete).toBe(true);
  });
});
