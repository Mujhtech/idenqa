export interface GrayFrame {
  readonly data: Uint8Array;
  readonly width: number;
  readonly height: number;
}

export interface PixelBuffer {
  readonly data: Uint8ClampedArray | Uint8Array;
  readonly width: number;
  readonly height: number;
}

export interface Point {
  readonly x: number;
  readonly y: number;
}

export interface DocumentQuad {
  readonly topLeft: Point;
  readonly topRight: Point;
  readonly bottomRight: Point;
  readonly bottomLeft: Point;
}

export interface DocumentDetection {
  readonly quad: DocumentQuad;
  /** Width in pixels of the frame the quad was detected in. */
  readonly frameWidth: number;
  /** Height in pixels of the frame the quad was detected in. */
  readonly frameHeight: number;
  /** Detected quad area divided by the frame area. */
  readonly coverage: number;
  /** Average long edge over average short edge, always >= 1. */
  readonly aspectRatio: number;
  readonly confidence: number;
}

export interface DocumentDetectionOptions {
  /** Longest edge of the grayscale buffer used for detection. */
  readonly maxDimension: number;
  /** Adaptive gradient threshold: mean + factor * standard deviation. */
  readonly gradientFactor: number;
  /** Absolute gradient floor below which a pixel is never an edge. */
  readonly minimumGradient: number;
  /** Upper bound on edge samples fed to the convex hull. */
  readonly maximumEdgeSamples: number;
  /** Detection-level rejection floor; the capture gate applies its own band. */
  readonly minimumCoverage: number;
  readonly maximumCoverage: number;
  readonly minimumAspectRatio: number;
  readonly maximumAspectRatio: number;
}

export const DOCUMENT_DETECTION_DEFAULTS: DocumentDetectionOptions = {
  maxDimension: 240,
  gradientFactor: 1.2,
  minimumGradient: 10,
  maximumEdgeSamples: 4096,
  minimumCoverage: 0.04,
  maximumCoverage: 1,
  minimumAspectRatio: 1.05,
  maximumAspectRatio: 3.5,
};

export interface DocumentFrameObservation {
  readonly detection: DocumentDetection | undefined;
  /** Variance of the Laplacian over the observed frame. */
  readonly sharpness: number;
  /** Normalised mean absolute difference against the previous observation. */
  readonly difference: number;
}

export type DocumentCaptureGateStatus = "searching" | "aligning" | "steadying" | "ready";

export type DocumentCaptureGateReason =
  | "no_document"
  | "low_coverage"
  | "high_coverage"
  | "out_of_frame"
  | "low_confidence"
  | "moving"
  | "blurry"
  | "unstable";

export interface DocumentCaptureGateOptions {
  /** Consecutive consistent frames required before automatic capture. */
  readonly requiredStableFrames: number;
  /** Maximum corner movement between frames as a fraction of the frame diagonal. */
  readonly maxCornerMovement: number;
  /** Maximum relative aspect-ratio change between consecutive frames. */
  readonly maxAspectDrift: number;
  /** Quad coverage band accepted for automatic capture. */
  readonly minCoverage: number;
  readonly maxCoverage: number;
  /** Minimum distance of every corner from the frame border, as a fraction of the short edge. */
  readonly minMargin: number;
  readonly minConfidence: number;
  /** Minimum variance-of-Laplacian below which a frame counts as too blurry. */
  readonly minSharpness: number;
  /** Maximum normalised frame-to-frame difference before a frame counts as motion. */
  readonly maxFrameDifference: number;
}

export const DOCUMENT_CAPTURE_GATE_DEFAULTS: DocumentCaptureGateOptions = {
  requiredStableFrames: 5,
  maxCornerMovement: 0.035,
  maxAspectDrift: 0.2,
  minCoverage: 0.3,
  maxCoverage: 0.97,
  minMargin: 0.01,
  minConfidence: 0.4,
  minSharpness: 40,
  maxFrameDifference: 0.03,
};

export interface DocumentCaptureGateResult {
  readonly status: DocumentCaptureGateStatus;
  readonly reason?: DocumentCaptureGateReason;
  readonly stableFrames: number;
  readonly requiredFrames: number;
}

export interface DocumentCaptureGate {
  observe(observation: DocumentFrameObservation): DocumentCaptureGateResult;
  reset(): void;
  readonly stableFrames: number;
}

export function rgbaToGrayFrame(
  image: PixelBuffer,
  maxDimension: number = DOCUMENT_DETECTION_DEFAULTS.maxDimension,
): GrayFrame {
  const { data, width, height } = image;
  if (
    width <= 0 ||
    height <= 0 ||
    !Number.isSafeInteger(width) ||
    !Number.isSafeInteger(height) ||
    data.length < width * height * 4
  ) {
    return { data: new Uint8Array(0), width: 0, height: 0 };
  }
  const scale = Math.min(1, maxDimension / Math.max(width, height));
  const outputWidth = Math.max(1, Math.round(width * scale));
  const outputHeight = Math.max(1, Math.round(height * scale));
  const gray = new Uint8Array(outputWidth * outputHeight);
  if (scale === 1) {
    for (let index = 0; index < gray.length; index += 1) {
      const offset = index * 4;
      gray[index] = Math.round(luminance(data[offset]!, data[offset + 1]!, data[offset + 2]!));
    }
    return { data: gray, width, height };
  }
  for (let y = 0; y < outputHeight; y += 1) {
    const sourceY0 = Math.min(height - 1, Math.floor((y * height) / outputHeight));
    const sourceY1 = Math.max(
      sourceY0 + 1,
      Math.min(height, Math.ceil(((y + 1) * height) / outputHeight)),
    );
    for (let x = 0; x < outputWidth; x += 1) {
      const sourceX0 = Math.min(width - 1, Math.floor((x * width) / outputWidth));
      const sourceX1 = Math.max(
        sourceX0 + 1,
        Math.min(width, Math.ceil(((x + 1) * width) / outputWidth)),
      );
      let sum = 0;
      let count = 0;
      for (let sourceY = sourceY0; sourceY < sourceY1; sourceY += 1) {
        for (let sourceX = sourceX0; sourceX < sourceX1; sourceX += 1) {
          const offset = (sourceY * width + sourceX) * 4;
          sum += luminance(data[offset]!, data[offset + 1]!, data[offset + 2]!);
          count += 1;
        }
      }
      gray[y * outputWidth + x] = count === 0 ? 0 : Math.round(sum / count);
    }
  }
  return { data: gray, width: outputWidth, height: outputHeight };
}

export function varianceOfLaplacian(
  frame: GrayFrame,
  region?: { readonly x0: number; readonly y0: number; readonly x1: number; readonly y1: number },
): number {
  const { data, width, height } = frame;
  if (width < 3 || height < 3) return 0;
  const x0 = Math.max(1, Math.floor(region?.x0 ?? 0));
  const y0 = Math.max(1, Math.floor(region?.y0 ?? 0));
  const x1 = Math.min(width - 2, Math.ceil(region?.x1 ?? width));
  const y1 = Math.min(height - 2, Math.ceil(region?.y1 ?? height));
  if (x1 < x0 || y1 < y0) return 0;
  let sum = 0;
  let sumSquares = 0;
  let count = 0;
  for (let y = y0; y <= y1; y += 1) {
    for (let x = x0; x <= x1; x += 1) {
      const index = y * width + x;
      const laplacian =
        4 * data[index]! -
        data[index - 1]! -
        data[index + 1]! -
        data[index - width]! -
        data[index + width]!;
      sum += laplacian;
      sumSquares += laplacian * laplacian;
      count += 1;
    }
  }
  if (count === 0) return 0;
  const mean = sum / count;
  return Math.max(0, sumSquares / count - mean * mean);
}

export function grayFrameDifference(previous: GrayFrame, current: GrayFrame): number {
  if (previous.width !== current.width || previous.height !== current.height) return 1;
  const length = Math.min(previous.data.length, current.data.length);
  if (length === 0) return 1;
  let sum = 0;
  for (let index = 0; index < length; index += 1) {
    sum += Math.abs(previous.data[index]! - current.data[index]!);
  }
  return sum / length / 255;
}

export function detectDocumentQuad(
  frame: GrayFrame,
  options: Partial<DocumentDetectionOptions> = {},
): DocumentDetection | undefined {
  const settings = { ...DOCUMENT_DETECTION_DEFAULTS, ...options };
  const { width, height } = frame;
  if (width < 8 || height < 8) return undefined;
  const magnitude = gradientMagnitude(frame);
  const threshold = adaptiveGradientThreshold(magnitude, settings);
  if (!Number.isFinite(threshold)) return undefined;
  const edgePoints = collectEdgePoints(magnitude, width, height, threshold, settings);
  if (edgePoints.length < 8) return undefined;
  const hull = convexHull(edgePoints);
  const quadPoints = reduceToQuad(hull);
  if (quadPoints === undefined) return undefined;
  const quad = orderQuad(quadPoints);
  if (quad === undefined) return undefined;

  const coverage = polygonArea(quad) / (width * height);
  const topLength = distance(quad.topLeft, quad.topRight);
  const bottomLength = distance(quad.bottomLeft, quad.bottomRight);
  const leftLength = distance(quad.topLeft, quad.bottomLeft);
  const rightLength = distance(quad.topRight, quad.bottomRight);
  const edgeWidth = (topLength + bottomLength) / 2;
  const edgeHeight = (leftLength + rightLength) / 2;
  if (edgeWidth <= 0 || edgeHeight <= 0) return undefined;
  const aspectRatio = Math.max(edgeWidth, edgeHeight) / Math.min(edgeWidth, edgeHeight);
  if (
    coverage < settings.minimumCoverage ||
    coverage > settings.maximumCoverage ||
    aspectRatio < settings.minimumAspectRatio ||
    aspectRatio > settings.maximumAspectRatio
  ) {
    return undefined;
  }
  const confidence = quadConfidence(quad, coverage, magnitude, width, height, threshold);
  return {
    quad,
    frameWidth: width,
    frameHeight: height,
    coverage,
    aspectRatio,
    confidence,
  };
}

export function createDocumentCaptureGate(
  options: Partial<DocumentCaptureGateOptions> = {},
): DocumentCaptureGate {
  const settings = normalizeDocumentCaptureGateOptions(options);
  let previous: DocumentDetection | undefined;
  let stableFrames = 0;

  return {
    get stableFrames() {
      return stableFrames;
    },
    reset() {
      previous = undefined;
      stableFrames = 0;
    },
    observe(observation) {
      const detection = observation.detection;
      if (detection === undefined) {
        previous = undefined;
        stableFrames = 0;
        return result(settings, "searching", 0, "no_document");
      }
      if (detection.coverage < settings.minCoverage) {
        previous = undefined;
        stableFrames = 0;
        return result(settings, "aligning", 0, "low_coverage");
      }
      if (detection.coverage > settings.maxCoverage) {
        previous = undefined;
        stableFrames = 0;
        return result(settings, "aligning", 0, "high_coverage");
      }
      if (!quadWithinMargin(detection, settings.minMargin)) {
        previous = undefined;
        stableFrames = 0;
        return result(settings, "aligning", 0, "out_of_frame");
      }
      if (detection.confidence < settings.minConfidence) {
        previous = undefined;
        stableFrames = 0;
        return result(settings, "aligning", 0, "low_confidence");
      }
      if (observation.difference > settings.maxFrameDifference) {
        previous = undefined;
        stableFrames = 0;
        return result(settings, "steadying", 0, "moving");
      }
      if (observation.sharpness < settings.minSharpness) {
        previous = undefined;
        stableFrames = 0;
        return result(settings, "steadying", 0, "blurry");
      }
      if (previous !== undefined && !consistentDetection(previous, detection, settings)) {
        previous = detection;
        stableFrames = 1;
        return result(settings, "steadying", stableFrames, "unstable");
      }
      previous = detection;
      stableFrames += 1;
      if (stableFrames >= settings.requiredStableFrames) {
        return result(settings, "ready", stableFrames);
      }
      return result(settings, "steadying", stableFrames, "unstable");
    },
  };
}

export function normalizeDocumentCaptureGateOptions(
  options: Partial<DocumentCaptureGateOptions> = {},
): DocumentCaptureGateOptions {
  return {
    requiredStableFrames: boundedNumber(
      options.requiredStableFrames,
      "requiredStableFrames",
      1,
      60,
      DOCUMENT_CAPTURE_GATE_DEFAULTS.requiredStableFrames,
    ),
    maxCornerMovement: boundedNumber(
      options.maxCornerMovement,
      "maxCornerMovement",
      0.001,
      1,
      DOCUMENT_CAPTURE_GATE_DEFAULTS.maxCornerMovement,
    ),
    maxAspectDrift: boundedNumber(
      options.maxAspectDrift,
      "maxAspectDrift",
      0.001,
      1,
      DOCUMENT_CAPTURE_GATE_DEFAULTS.maxAspectDrift,
    ),
    minCoverage: boundedNumber(
      options.minCoverage,
      "minCoverage",
      0.01,
      1,
      DOCUMENT_CAPTURE_GATE_DEFAULTS.minCoverage,
    ),
    maxCoverage: boundedNumber(
      options.maxCoverage,
      "maxCoverage",
      0.01,
      1,
      DOCUMENT_CAPTURE_GATE_DEFAULTS.maxCoverage,
    ),
    minMargin: boundedNumber(
      options.minMargin,
      "minMargin",
      0,
      0.25,
      DOCUMENT_CAPTURE_GATE_DEFAULTS.minMargin,
    ),
    minConfidence: boundedNumber(
      options.minConfidence,
      "minConfidence",
      0,
      1,
      DOCUMENT_CAPTURE_GATE_DEFAULTS.minConfidence,
    ),
    minSharpness: boundedNumber(
      options.minSharpness,
      "minSharpness",
      0,
      100_000,
      DOCUMENT_CAPTURE_GATE_DEFAULTS.minSharpness,
    ),
    maxFrameDifference: boundedNumber(
      options.maxFrameDifference,
      "maxFrameDifference",
      0,
      1,
      DOCUMENT_CAPTURE_GATE_DEFAULTS.maxFrameDifference,
    ),
  };
}

export function normalizeDocumentDetectionOptions(
  options: Partial<DocumentDetectionOptions> = {},
): DocumentDetectionOptions {
  return {
    maxDimension: boundedNumber(
      options.maxDimension,
      "maxDimension",
      32,
      1024,
      DOCUMENT_DETECTION_DEFAULTS.maxDimension,
    ),
    gradientFactor: boundedNumber(
      options.gradientFactor,
      "gradientFactor",
      0,
      10,
      DOCUMENT_DETECTION_DEFAULTS.gradientFactor,
    ),
    minimumGradient: boundedNumber(
      options.minimumGradient,
      "minimumGradient",
      0,
      255,
      DOCUMENT_DETECTION_DEFAULTS.minimumGradient,
    ),
    maximumEdgeSamples: boundedNumber(
      options.maximumEdgeSamples,
      "maximumEdgeSamples",
      16,
      65_536,
      DOCUMENT_DETECTION_DEFAULTS.maximumEdgeSamples,
    ),
    minimumCoverage: boundedNumber(
      options.minimumCoverage,
      "minimumCoverage",
      0,
      1,
      DOCUMENT_DETECTION_DEFAULTS.minimumCoverage,
    ),
    maximumCoverage: boundedNumber(
      options.maximumCoverage,
      "maximumCoverage",
      0,
      1,
      DOCUMENT_DETECTION_DEFAULTS.maximumCoverage,
    ),
    minimumAspectRatio: boundedNumber(
      options.minimumAspectRatio,
      "minimumAspectRatio",
      1,
      10,
      DOCUMENT_DETECTION_DEFAULTS.minimumAspectRatio,
    ),
    maximumAspectRatio: boundedNumber(
      options.maximumAspectRatio,
      "maximumAspectRatio",
      1,
      10,
      DOCUMENT_DETECTION_DEFAULTS.maximumAspectRatio,
    ),
  };
}

export function scaleDocumentQuad(
  quad: DocumentQuad,
  scaleX: number,
  scaleY: number,
): DocumentQuad {
  return {
    topLeft: scalePoint(quad.topLeft, scaleX, scaleY),
    topRight: scalePoint(quad.topRight, scaleX, scaleY),
    bottomRight: scalePoint(quad.bottomRight, scaleX, scaleY),
    bottomLeft: scalePoint(quad.bottomLeft, scaleX, scaleY),
  };
}

function luminance(red: number, green: number, blue: number): number {
  return 0.299 * red + 0.587 * green + 0.114 * blue;
}

function scalePoint(point: Point, scaleX: number, scaleY: number): Point {
  return { x: point.x * scaleX, y: point.y * scaleY };
}

function gradientMagnitude(frame: GrayFrame): Float32Array {
  const { data, width, height } = frame;
  const magnitude = new Float32Array(width * height);
  for (let y = 1; y < height - 1; y += 1) {
    for (let x = 1; x < width - 1; x += 1) {
      const index = y * width + x;
      const topLeft = data[index - width - 1]!;
      const top = data[index - width]!;
      const topRight = data[index - width + 1]!;
      const left = data[index - 1]!;
      const right = data[index + 1]!;
      const bottomLeft = data[index + width - 1]!;
      const bottom = data[index + width]!;
      const bottomRight = data[index + width + 1]!;
      const gradientX = topRight + 2 * right + bottomRight - (topLeft + 2 * left + bottomLeft);
      const gradientY = bottomLeft + 2 * bottom + bottomRight - (topLeft + 2 * top + topRight);
      magnitude[index] = Math.hypot(gradientX, gradientY);
    }
  }
  return magnitude;
}

function adaptiveGradientThreshold(
  magnitude: Float32Array,
  options: DocumentDetectionOptions,
): number {
  let sum = 0;
  let sumSquares = 0;
  for (let index = 0; index < magnitude.length; index += 1) {
    const value = magnitude[index]!;
    sum += value;
    sumSquares += value * value;
  }
  const count = magnitude.length;
  if (count === 0) return Number.POSITIVE_INFINITY;
  const mean = sum / count;
  const variance = Math.max(0, sumSquares / count - mean * mean);
  return Math.max(options.minimumGradient, mean + options.gradientFactor * Math.sqrt(variance));
}

function collectEdgePoints(
  magnitude: Float32Array,
  width: number,
  height: number,
  threshold: number,
  options: DocumentDetectionOptions,
): Point[] {
  const candidates: Point[] = [];
  for (let y = 1; y < height - 1; y += 1) {
    for (let x = 1; x < width - 1; x += 1) {
      if (magnitude[y * width + x]! >= threshold) candidates.push({ x, y });
    }
  }
  if (candidates.length <= options.maximumEdgeSamples) return candidates;
  const stride = Math.ceil(candidates.length / options.maximumEdgeSamples);
  const sampled: Point[] = [];
  for (let index = 0; index < candidates.length; index += stride) {
    sampled.push(candidates[index]!);
  }
  return sampled;
}

function convexHull(points: readonly Point[]): Point[] {
  const sorted = [...points].sort((left, right) => left.x - right.x || left.y - right.y);
  if (sorted.length < 3) return [...sorted];
  const lower: Point[] = [];
  for (const point of sorted) {
    while (
      lower.length >= 2 &&
      cross(lower[lower.length - 2]!, lower[lower.length - 1]!, point) <= 0
    ) {
      lower.pop();
    }
    lower.push(point);
  }
  const upper: Point[] = [];
  for (let index = sorted.length - 1; index >= 0; index -= 1) {
    const point = sorted[index]!;
    while (
      upper.length >= 2 &&
      cross(upper[upper.length - 2]!, upper[upper.length - 1]!, point) <= 0
    ) {
      upper.pop();
    }
    upper.push(point);
  }
  lower.pop();
  upper.pop();
  return lower.concat(upper);
}

function reduceToQuad(hull: readonly Point[]): Point[] | undefined {
  if (hull.length < 4) return undefined;
  let points = [...hull];
  if (points.length > 512) {
    const stride = Math.ceil(points.length / 512);
    points = points.filter((_, index) => index % stride === 0);
    if (points.length < 4) return undefined;
  }
  while (points.length > 4) {
    let removeIndex = 0;
    let smallestArea = Number.POSITIVE_INFINITY;
    for (let index = 0; index < points.length; index += 1) {
      const previous = points[(index - 1 + points.length) % points.length]!;
      const current = points[index]!;
      const next = points[(index + 1) % points.length]!;
      const area = Math.abs(triangleArea(previous, current, next));
      if (area < smallestArea) {
        smallestArea = area;
        removeIndex = index;
      }
    }
    points.splice(removeIndex, 1);
  }
  let area = 0;
  for (let index = 0; index < points.length; index += 1) {
    const current = points[index]!;
    const next = points[(index + 1) % points.length]!;
    area += current.x * next.y - next.x * current.y;
  }
  return Math.abs(area) / 2 < 1 ? undefined : points;
}

function orderQuad(points: readonly Point[]): DocumentQuad | undefined {
  if (points.length !== 4) return undefined;
  let topLeft: Point | undefined;
  let topRight: Point | undefined;
  let bottomRight: Point | undefined;
  let bottomLeft: Point | undefined;
  for (const point of points) {
    if (topLeft === undefined || point.x + point.y < topLeft.x + topLeft.y) topLeft = point;
    if (bottomRight === undefined || point.x + point.y > bottomRight.x + bottomRight.y) {
      bottomRight = point;
    }
    if (topRight === undefined || point.x - point.y > topRight.x - topRight.y) topRight = point;
    if (bottomLeft === undefined || point.x - point.y < bottomLeft.x - bottomLeft.y) {
      bottomLeft = point;
    }
  }
  const candidates = [topLeft, topRight, bottomRight, bottomLeft];
  if (new Set(candidates).size !== 4) return orderQuadByAngle(points);
  if (cross(topLeft!, topRight!, bottomRight!) < 0) {
    [topRight, bottomLeft] = [bottomLeft, topRight];
  }
  const ordered: DocumentQuad = {
    topLeft: topLeft!,
    topRight: topRight!,
    bottomRight: bottomRight!,
    bottomLeft: bottomLeft!,
  };
  return isConvexQuad(ordered) ? ordered : orderQuadByAngle(points);
}

function orderQuadByAngle(points: readonly Point[]): DocumentQuad | undefined {
  if (points.length !== 4) return undefined;
  const centerX = points.reduce((sum, point) => sum + point.x, 0) / points.length;
  const centerY = points.reduce((sum, point) => sum + point.y, 0) / points.length;
  const sorted = [...points].sort(
    (left, right) =>
      Math.atan2(left.y - centerY, left.x - centerX) -
      Math.atan2(right.y - centerY, right.x - centerX),
  );
  let start = 0;
  for (let index = 1; index < sorted.length; index += 1) {
    const candidate = sorted[index]!;
    const current = sorted[start]!;
    if (
      candidate.x + candidate.y < current.x + current.y ||
      (candidate.x + candidate.y === current.x + current.y && candidate.x < current.x)
    ) {
      start = index;
    }
  }
  let rotated = [...sorted.slice(start), ...sorted.slice(0, start)];
  if (cross(rotated[0]!, rotated[1]!, rotated[2]!) < 0) {
    rotated = [rotated[0]!, ...rotated.slice(1).reverse()];
  }
  const quad: DocumentQuad = {
    topLeft: rotated[0]!,
    topRight: rotated[1]!,
    bottomRight: rotated[2]!,
    bottomLeft: rotated[3]!,
  };
  return isConvexQuad(quad) ? quad : undefined;
}

function isConvexQuad(quad: DocumentQuad): boolean {
  const points = [quad.topLeft, quad.topRight, quad.bottomRight, quad.bottomLeft];
  let positive = 0;
  let negative = 0;
  for (let index = 0; index < points.length; index += 1) {
    const value = cross(
      points[index]!,
      points[(index + 1) % points.length]!,
      points[(index + 2) % points.length]!,
    );
    if (value > 0) positive += 1;
    if (value < 0) negative += 1;
  }
  return positive === 4 || negative === 4;
}

function quadConfidence(
  quad: DocumentQuad,
  coverage: number,
  magnitude: Float32Array,
  width: number,
  height: number,
  threshold: number,
): number {
  const edges: readonly (readonly [Point, Point])[] = [
    [quad.topLeft, quad.topRight],
    [quad.topRight, quad.bottomRight],
    [quad.bottomRight, quad.bottomLeft],
    [quad.bottomLeft, quad.topLeft],
  ];
  const sampleCount = 24;
  let supported = 0;
  let total = 0;
  const tolerance = threshold * 0.6;
  for (const [start, end] of edges) {
    for (let sample = 0; sample <= sampleCount; sample += 1) {
      const position = sample / sampleCount;
      const x = Math.round(start.x + (end.x - start.x) * position);
      const y = Math.round(start.y + (end.y - start.y) * position);
      if (x < 0 || y < 0 || x >= width || y >= height) continue;
      total += 1;
      if (magnitude[y * width + x]! >= tolerance) supported += 1;
    }
  }
  const edgeSupport = total === 0 ? 0 : supported / total;
  const coverageScore =
    coverage < 0.35 ? coverage / 0.35 : coverage > 0.9 ? (1 - coverage) / 0.1 : 1;
  const angleScore =
    corners
      .map((corner) => angleQuality(quad[corner], adjacentCorners(quad, corner)))
      .reduce((sum, value) => sum + value, 0) / 4;
  return clamp01(0.55 * edgeSupport + 0.25 * clamp01(coverageScore) + 0.2 * clamp01(angleScore));
}

const corners = ["topLeft", "topRight", "bottomRight", "bottomLeft"] as const;
type CornerName = (typeof corners)[number];

function adjacentCorners(quad: DocumentQuad, corner: CornerName): readonly [Point, Point] {
  const points = [quad.topLeft, quad.topRight, quad.bottomRight, quad.bottomLeft];
  const index = corners.indexOf(corner);
  return [points[(index + 3) % 4]!, points[(index + 1) % 4]!];
}

function angleQuality(corner: Point, adjacent: readonly [Point, Point]): number {
  const [previous, next] = adjacent;
  const first = { x: previous.x - corner.x, y: previous.y - corner.y };
  const second = { x: next.x - corner.x, y: next.y - corner.y };
  const dot = first.x * second.x + first.y * second.y;
  const crossValue = first.x * second.y - first.y * second.x;
  const angle = Math.abs(Math.atan2(crossValue, dot));
  return clamp01(1 - Math.abs(angle - Math.PI / 2) / (Math.PI / 3));
}

function quadWithinMargin(detection: DocumentDetection, minMargin: number): boolean {
  const marginX = minMargin * Math.min(detection.frameWidth, detection.frameHeight);
  const marginY = marginX;
  const points = [
    detection.quad.topLeft,
    detection.quad.topRight,
    detection.quad.bottomRight,
    detection.quad.bottomLeft,
  ];
  return points.every((point) => {
    return (
      point.x >= marginX &&
      point.y >= marginY &&
      point.x <= detection.frameWidth - 1 - marginX &&
      point.y <= detection.frameHeight - 1 - marginY
    );
  });
}

function consistentDetection(
  previous: DocumentDetection,
  current: DocumentDetection,
  options: DocumentCaptureGateOptions,
): boolean {
  const previousDiagonal = Math.hypot(previous.frameWidth, previous.frameHeight);
  const currentDiagonal = Math.hypot(current.frameWidth, current.frameHeight);
  const previousPoints = [
    previous.quad.topLeft,
    previous.quad.topRight,
    previous.quad.bottomRight,
    previous.quad.bottomLeft,
  ];
  const currentPoints = [
    current.quad.topLeft,
    current.quad.topRight,
    current.quad.bottomRight,
    current.quad.bottomLeft,
  ];
  let movement = 0;
  for (let index = 0; index < previousPoints.length; index += 1) {
    const previousPoint = previousPoints[index]!;
    const currentPoint = currentPoints[index]!;
    const diagonal = Math.max(1, (previousDiagonal + currentDiagonal) / 2);
    movement = Math.max(
      movement,
      Math.hypot(currentPoint.x - previousPoint.x, currentPoint.y - previousPoint.y) / diagonal,
    );
  }
  const aspectDrift =
    previous.aspectRatio <= 0
      ? 1
      : Math.abs(current.aspectRatio - previous.aspectRatio) / previous.aspectRatio;
  return movement <= options.maxCornerMovement && aspectDrift <= options.maxAspectDrift;
}

function result(
  options: DocumentCaptureGateOptions,
  status: DocumentCaptureGateStatus,
  stableFrames: number,
  reason?: DocumentCaptureGateReason,
): DocumentCaptureGateResult {
  return {
    status,
    ...(reason === undefined ? {} : { reason }),
    stableFrames,
    requiredFrames: options.requiredStableFrames,
  };
}

export function polygonArea(quad: DocumentQuad): number {
  const points = [quad.topLeft, quad.topRight, quad.bottomRight, quad.bottomLeft];
  let sum = 0;
  for (let index = 0; index < points.length; index += 1) {
    const current = points[index]!;
    const next = points[(index + 1) % points.length]!;
    sum += current.x * next.y - next.x * current.y;
  }
  return Math.abs(sum) / 2;
}

export function distance(left: Point, right: Point): number {
  return Math.hypot(right.x - left.x, right.y - left.y);
}

function triangleArea(first: Point, second: Point, third: Point): number {
  return (
    ((second.x - first.x) * (third.y - first.y) - (second.y - first.y) * (third.x - first.x)) / 2
  );
}

function cross(origin: Point, first: Point, second: Point): number {
  return (
    (first.x - origin.x) * (second.y - origin.y) - (first.y - origin.y) * (second.x - origin.x)
  );
}

function clamp01(value: number): number {
  return Math.min(1, Math.max(0, value));
}

function boundedNumber(
  value: number | undefined,
  name: string,
  minimum: number,
  maximum: number,
  fallback: number,
): number {
  if (value === undefined) return fallback;
  if (!Number.isFinite(value) || value < minimum || value > maximum) {
    throw new TypeError(`${name} must be a finite number between ${minimum} and ${maximum}.`);
  }
  return value;
}
