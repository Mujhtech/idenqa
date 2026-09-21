import { describe, expect, it } from "vitest";

import {
  DOCUMENT_CAPTURE_GATE_DEFAULTS,
  createDocumentCaptureGate,
  detectDocumentQuad,
  grayFrameDifference,
  normalizeDocumentCaptureGateOptions,
  polygonArea,
  rgbaToGrayFrame,
  varianceOfLaplacian,
  type DocumentDetection,
  type DocumentQuad,
  type GrayFrame,
  type Point,
} from "../src/index.js";

const FRAME_WIDTH = 320;
const FRAME_HEIGHT = 240;

describe("rgbaToGrayFrame", () => {
  it("converts RGBA pixels to BT.601 luminance", () => {
    const data = new Uint8ClampedArray([
      255, 0, 0, 255, 0, 255, 0, 255, 0, 0, 255, 255, 255, 255, 255, 255,
    ]);
    const frame = rgbaToGrayFrame({ data, width: 4, height: 1 }, 8);
    expect([...frame.data]).toEqual([76, 150, 29, 255]);
  });

  it("box-downscales a bounded frame while preserving a known average", () => {
    const data = new Uint8ClampedArray(8 * 2 * 4);
    for (let index = 0; index < 8 * 2; index += 1) {
      const value = index % 2 === 0 ? 100 : 200;
      data[index * 4] = value;
      data[index * 4 + 1] = value;
      data[index * 4 + 2] = value;
      data[index * 4 + 3] = 255;
    }
    const frame = rgbaToGrayFrame({ data, width: 8, height: 2 }, 2);
    expect(frame.width).toBe(2);
    expect(frame.height).toBe(1);
    expect([...frame.data]).toEqual([150, 150]);
  });

  it("returns an empty frame for malformed input", () => {
    expect(rgbaToGrayFrame({ data: new Uint8ClampedArray(0), width: 0, height: 0 }, 8)).toEqual({
      data: new Uint8Array(0),
      width: 0,
      height: 0,
    });
  });
});

describe("detectDocumentQuad", () => {
  it("finds an axis-aligned document with ordered corners", () => {
    const frame = grayFrame(FRAME_WIDTH, FRAME_HEIGHT, 0);
    paintPolygon(frame, rectangle(60, 50, 260, 190), 240);

    const detection = detectDocumentQuad(frame);
    expect(detection).toBeDefined();
    expect(detection!.aspectRatio).toBeCloseTo(200 / 140, 1);
    expect(detection!.coverage).toBeCloseTo((200 * 140) / (FRAME_WIDTH * FRAME_HEIGHT), 1);
    expect(detection!.confidence).toBeGreaterThan(0.4);
    expectPointClose(detection!.quad.topLeft, { x: 60, y: 50 }, 5);
    expectPointClose(detection!.quad.topRight, { x: 260, y: 50 }, 5);
    expectPointClose(detection!.quad.bottomRight, { x: 260, y: 190 }, 5);
    expectPointClose(detection!.quad.bottomLeft, { x: 60, y: 190 }, 5);
  });

  it("finds a perspective-distorted document", () => {
    const frame = grayFrame(FRAME_WIDTH, FRAME_HEIGHT, 0);
    const corners: readonly Point[] = [
      { x: 50, y: 40 },
      { x: 290, y: 60 },
      { x: 270, y: 215 },
      { x: 35, y: 195 },
    ];
    paintPolygon(frame, corners, 235);

    const detection = detectDocumentQuad(frame);
    expect(detection).toBeDefined();
    expect(detection!.aspectRatio).toBeGreaterThan(1.3);
    expect(detection!.aspectRatio).toBeLessThan(1.9);
    expect(detection!.coverage).toBeGreaterThan(0.35);
    expect(detection!.confidence).toBeGreaterThan(0.4);
    expectCornerOrder(detection!.quad);
    expectPointClose(detection!.quad.topLeft, corners[0]!, 6);
    expectPointClose(detection!.quad.topRight, corners[1]!, 6);
    expectPointClose(detection!.quad.bottomRight, corners[2]!, 6);
    expectPointClose(detection!.quad.bottomLeft, corners[3]!, 6);
  });

  it("ignores a frame without a document", () => {
    const ramp = grayFrame(FRAME_WIDTH, FRAME_HEIGHT, 0);
    for (let y = 0; y < FRAME_HEIGHT; y += 1) {
      for (let x = 0; x < FRAME_WIDTH; x += 1) {
        ramp.data[y * FRAME_WIDTH + x] = Math.round(80 + (x / FRAME_WIDTH) * 120);
      }
    }
    expect(detectDocumentQuad(ramp)).toBeUndefined();
    expect(detectDocumentQuad(grayFrame(FRAME_WIDTH, FRAME_HEIGHT, 200))).toBeUndefined();
  });

  it("ignores a low-contrast blurred frame", () => {
    const frame = grayFrame(FRAME_WIDTH, FRAME_HEIGHT, 120);
    paintPolygon(frame, rectangle(50, 40, 280, 210), 126);
    boxBlur(frame, 6);
    expect(detectDocumentQuad(frame)).toBeUndefined();
  });

  it("rejects quads below the detection coverage floor", () => {
    const frame = grayFrame(FRAME_WIDTH, FRAME_HEIGHT, 0);
    paintPolygon(frame, rectangle(150, 110, 170, 130), 240);
    expect(detectDocumentQuad(frame)).toBeUndefined();
  });
});

describe("varianceOfLaplacian", () => {
  it("separates a sharp edge from a blurred one", () => {
    const sharp = grayFrame(FRAME_WIDTH, FRAME_HEIGHT, 0);
    paintPolygon(sharp, rectangle(40, 30, 280, 210), 240);
    const blurred = grayFrame(FRAME_WIDTH, FRAME_HEIGHT, 0);
    paintPolygon(blurred, rectangle(40, 30, 280, 210), 240);
    boxBlur(blurred, 4);

    const sharpScore = varianceOfLaplacian(sharp);
    const blurredScore = varianceOfLaplacian(blurred);
    expect(sharpScore).toBeGreaterThan(1000);
    expect(blurredScore).toBeLessThan(sharpScore / 4);
    expect(blurredScore).toBeLessThan(DOCUMENT_CAPTURE_GATE_DEFAULTS.minSharpness);
  });

  it("returns zero for frames without an interior", () => {
    expect(varianceOfLaplacian(grayFrame(2, 2, 128))).toBe(0);
  });
});

describe("grayFrameDifference", () => {
  it("is zero for identical frames and grows with movement", () => {
    const first = grayFrame(FRAME_WIDTH, FRAME_HEIGHT, 0);
    paintPolygon(first, rectangle(40, 30, 280, 210), 240);
    const identical = grayFrame(FRAME_WIDTH, FRAME_HEIGHT, 0);
    paintPolygon(identical, rectangle(40, 30, 280, 210), 240);
    const moved = grayFrame(FRAME_WIDTH, FRAME_HEIGHT, 0);
    paintPolygon(moved, rectangle(55, 30, 295, 210), 240);

    expect(grayFrameDifference(first, identical)).toBe(0);
    expect(grayFrameDifference(first, moved)).toBeGreaterThan(
      DOCUMENT_CAPTURE_GATE_DEFAULTS.maxFrameDifference,
    );
  });

  it("treats mismatched frame sizes as a full change", () => {
    expect(grayFrameDifference(grayFrame(4, 4, 0), grayFrame(8, 8, 0))).toBe(1);
  });
});

describe("createDocumentCaptureGate", () => {
  it("reaches ready only after the required number of consistent frames", () => {
    const gate = testGate({ requiredStableFrames: 3 });
    const detection = detectionAt(0.5);

    expect(gate.observe(observation(detection))).toMatchObject({
      status: "steadying",
      stableFrames: 1,
    });
    expect(gate.observe(observation(detection))).toMatchObject({
      status: "steadying",
      stableFrames: 2,
    });
    expect(gate.observe(observation(detection))).toMatchObject({
      status: "ready",
      stableFrames: 3,
    });
  });

  it("restarts stability after corner movement and never fires early", () => {
    const gate = testGate({ requiredStableFrames: 3, maxCornerMovement: 0.02 });
    const steady = detectionAt(0.5);
    expect(gate.observe(observation(steady)).status).toBe("steadying");
    expect(gate.observe(observation(steady)).status).toBe("steadying");

    const jittered = detectionAt(0.5, (quad) => translateQuad(quad, 20, 8));
    expect(gate.observe(observation(jittered))).toMatchObject({
      status: "steadying",
      reason: "unstable",
      stableFrames: 1,
    });
    expect(gate.observe(observation(jittered)).status).toBe("steadying");
    expect(gate.observe(observation(jittered)).status).toBe("ready");
  });

  it("never fires for missing, small, clipped, or low-confidence documents", () => {
    const gate = testGate({ requiredStableFrames: 2 });
    for (let attempt = 0; attempt < 10; attempt += 1) {
      expect(gate.observe(observation(undefined))).toMatchObject({
        status: "searching",
        reason: "no_document",
      });
    }
    for (let attempt = 0; attempt < 10; attempt += 1) {
      expect(gate.observe(observation(detectionAt(0.2)))).toMatchObject({
        status: "aligning",
        reason: "low_coverage",
      });
    }
    const clipped = detectionAt(0.5, (quad) => ({
      topLeft: { x: 0, y: quad.topLeft.y },
      topRight: quad.topRight,
      bottomRight: quad.bottomRight,
      bottomLeft: quad.bottomLeft,
    }));
    for (let attempt = 0; attempt < 10; attempt += 1) {
      expect(gate.observe(observation(clipped))).toMatchObject({
        status: "aligning",
        reason: "out_of_frame",
      });
    }
    for (let attempt = 0; attempt < 10; attempt += 1) {
      expect(gate.observe(observation(detectionAt(0.5, undefined, 0.1)))).toMatchObject({
        status: "aligning",
        reason: "low_confidence",
      });
    }
  });

  it("never fires for blurry or moving frames", () => {
    const gate = testGate({ requiredStableFrames: 2 });
    for (let attempt = 0; attempt < 10; attempt += 1) {
      expect(gate.observe(observation(detectionAt(0.5), { sharpness: 5 }))).toMatchObject({
        status: "steadying",
        reason: "blurry",
      });
    }
    for (let attempt = 0; attempt < 10; attempt += 1) {
      expect(gate.observe(observation(detectionAt(0.5), { difference: 0.2 }))).toMatchObject({
        status: "steadying",
        reason: "moving",
      });
    }
  });

  it("resets accumulated stability explicitly", () => {
    const gate = testGate({ requiredStableFrames: 2 });
    gate.observe(observation(detectionAt(0.5)));
    expect(gate.stableFrames).toBe(1);
    gate.reset();
    expect(gate.stableFrames).toBe(0);
    expect(gate.observe(observation(detectionAt(0.5))).stableFrames).toBe(1);
  });

  it("validates option ranges", () => {
    expect(() => normalizeDocumentCaptureGateOptions({ requiredStableFrames: 0 })).toThrow(
      TypeError,
    );
    expect(() => normalizeDocumentCaptureGateOptions({ minCoverage: 2 })).toThrow(TypeError);
  });
});

function testGate(options: Parameters<typeof createDocumentCaptureGate>[0]) {
  return createDocumentCaptureGate({
    minConfidence: 0.2,
    minSharpness: 40,
    maxFrameDifference: 0.03,
    ...options,
  });
}

function observation(
  detection: DocumentDetection | undefined,
  overrides: { readonly sharpness?: number; readonly difference?: number } = {},
) {
  return {
    detection,
    sharpness: overrides.sharpness ?? 800,
    difference: overrides.difference ?? 0,
  };
}

function detectionAt(
  coverage: number,
  transform?: (quad: DocumentQuad) => DocumentQuad,
  confidence = 0.8,
): DocumentDetection {
  const base = axisQuad();
  const quad = transform === undefined ? base : transform(base);
  return {
    quad,
    frameWidth: FRAME_WIDTH,
    frameHeight: FRAME_HEIGHT,
    coverage,
    aspectRatio: 1.4,
    confidence,
  };
}

function translateQuad(quad: DocumentQuad, dx: number, dy: number): DocumentQuad {
  return {
    topLeft: { x: quad.topLeft.x + dx, y: quad.topLeft.y + dy },
    topRight: { x: quad.topRight.x + dx, y: quad.topRight.y + dy },
    bottomRight: { x: quad.bottomRight.x + dx, y: quad.bottomRight.y + dy },
    bottomLeft: { x: quad.bottomLeft.x + dx, y: quad.bottomLeft.y + dy },
  };
}

function grayFrame(width: number, height: number, fill: number): GrayFrame {
  const data = new Uint8Array(width * height);
  data.fill(fill);
  return { data, width, height };
}

function rectangle(left: number, top: number, right: number, bottom: number): Point[] {
  return [
    { x: left, y: top },
    { x: right, y: top },
    { x: right, y: bottom },
    { x: left, y: bottom },
  ];
}

function axisQuad(): DocumentQuad {
  return {
    topLeft: { x: 60, y: 45 },
    topRight: { x: 260, y: 45 },
    bottomRight: { x: 260, y: 195 },
    bottomLeft: { x: 60, y: 195 },
  };
}

function paintPolygon(frame: GrayFrame, corners: readonly Point[], value: number): void {
  const minimumY = Math.max(0, Math.floor(Math.min(...corners.map((point) => point.y))));
  const maximumY = Math.min(
    frame.height - 1,
    Math.ceil(Math.max(...corners.map((point) => point.y))),
  );
  for (let y = minimumY; y <= maximumY; y += 1) {
    const intersections: number[] = [];
    for (let index = 0; index < corners.length; index += 1) {
      const start = corners[index]!;
      const end = corners[(index + 1) % corners.length]!;
      if ((start.y <= y && end.y > y) || (end.y <= y && start.y > y)) {
        const position = (y - start.y) / (end.y - start.y);
        intersections.push(start.x + position * (end.x - start.x));
      }
    }
    intersections.sort((left, right) => left - right);
    for (let index = 0; index + 1 < intersections.length; index += 2) {
      const from = Math.max(0, Math.ceil(intersections[index]!));
      const to = Math.min(frame.width - 1, Math.floor(intersections[index + 1]!));
      for (let x = from; x <= to; x += 1) frame.data[y * frame.width + x] = value;
    }
  }
}

function boxBlur(frame: GrayFrame, radius: number): void {
  const source = new Uint8Array(frame.data);
  const { width, height } = frame;
  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      let sum = 0;
      let count = 0;
      for (let offsetY = -radius; offsetY <= radius; offsetY += 1) {
        for (let offsetX = -radius; offsetX <= radius; offsetX += 1) {
          const sampleX = x + offsetX;
          const sampleY = y + offsetY;
          if (sampleX < 0 || sampleY < 0 || sampleX >= width || sampleY >= height) continue;
          sum += source[sampleY * width + sampleX]!;
          count += 1;
        }
      }
      frame.data[y * width + x] = Math.round(sum / count);
    }
  }
}

function expectPointClose(actual: Point, expected: Point, tolerance: number): void {
  expect(Math.abs(actual.x - expected.x)).toBeLessThanOrEqual(tolerance);
  expect(Math.abs(actual.y - expected.y)).toBeLessThanOrEqual(tolerance);
}

function expectCornerOrder(quad: DocumentQuad): void {
  expect(polygonArea(quad)).toBeGreaterThan(0);
  expect(quad.topLeft.x).toBeLessThan(quad.topRight.x);
  expect(quad.topLeft.y).toBeLessThan(quad.bottomLeft.y);
  expect(quad.topRight.y).toBeLessThan(quad.bottomRight.y);
}
