import type { DocumentDetection, DocumentQuad, PixelBuffer, Point } from "./document-detection.js";
import { distance, scaleDocumentQuad } from "./document-detection.js";

export interface DocumentCorrectionOptions {
  /** Longest edge of the corrected image. */
  readonly maxDimension: number;
  /** Maximum number of pixels in the corrected image. */
  readonly maxArea: number;
  /** Aspect-ratio clamp applied to the detected quad's average aspect. */
  readonly minAspect: number;
  readonly maxAspect: number;
  readonly minimumOutputEdge: number;
  /** Grayscale value used outside the corrected quad. */
  readonly background: number;
}

export const DOCUMENT_CORRECTION_DEFAULTS: DocumentCorrectionOptions = {
  maxDimension: 2048,
  maxArea: 4_000_000,
  minAspect: 1.2,
  maxAspect: 1.9,
  minimumOutputEdge: 64,
  background: 255,
};

export interface CorrectedDocumentFrame {
  readonly image: {
    readonly data: Uint8ClampedArray<ArrayBuffer>;
    readonly width: number;
    readonly height: number;
  };
  readonly quad: DocumentQuad;
  readonly width: number;
  readonly height: number;
}

export interface DocumentCorrectionSize {
  readonly width: number;
  readonly height: number;
  readonly quad: DocumentQuad;
}

export type DocumentHomography = readonly number[];

export function normalizeDocumentCorrectionOptions(
  options: Partial<DocumentCorrectionOptions> = {},
): DocumentCorrectionOptions {
  const normalized: DocumentCorrectionOptions = {
    maxDimension: boundedInteger(
      options.maxDimension,
      "maxDimension",
      256,
      4096,
      DOCUMENT_CORRECTION_DEFAULTS.maxDimension,
    ),
    maxArea: boundedInteger(
      options.maxArea,
      "maxArea",
      65_536,
      16_777_216,
      DOCUMENT_CORRECTION_DEFAULTS.maxArea,
    ),
    minAspect: boundedNumber(
      options.minAspect,
      "minAspect",
      1,
      1.5,
      DOCUMENT_CORRECTION_DEFAULTS.minAspect,
    ),
    maxAspect: boundedNumber(
      options.maxAspect,
      "maxAspect",
      1.5,
      3,
      DOCUMENT_CORRECTION_DEFAULTS.maxAspect,
    ),
    minimumOutputEdge: boundedInteger(
      options.minimumOutputEdge,
      "minimumOutputEdge",
      16,
      512,
      DOCUMENT_CORRECTION_DEFAULTS.minimumOutputEdge,
    ),
    background: boundedInteger(
      options.background,
      "background",
      0,
      255,
      DOCUMENT_CORRECTION_DEFAULTS.background,
    ),
  };
  if (normalized.minAspect > normalized.maxAspect) {
    throw new TypeError("minAspect must not exceed maxAspect.");
  }
  return normalized;
}

export function orientDocumentQuad(quad: DocumentQuad): {
  readonly quad: DocumentQuad;
  readonly width: number;
  readonly height: number;
} {
  const topLength = distance(quad.topLeft, quad.topRight);
  const bottomLength = distance(quad.bottomLeft, quad.bottomRight);
  const leftLength = distance(quad.topLeft, quad.bottomLeft);
  const rightLength = distance(quad.topRight, quad.bottomRight);
  const width = (topLength + bottomLength) / 2;
  const height = (leftLength + rightLength) / 2;
  if (width < height) {
    return {
      quad: {
        topLeft: quad.bottomLeft,
        topRight: quad.topLeft,
        bottomRight: quad.topRight,
        bottomLeft: quad.bottomRight,
      },
      width: height,
      height: width,
    };
  }
  return { quad, width, height };
}

export function documentCorrectionSize(
  quad: DocumentQuad,
  options: Partial<DocumentCorrectionOptions> = {},
): DocumentCorrectionSize | undefined {
  const settings = normalizeDocumentCorrectionOptions(options);
  const oriented = orientDocumentQuad(quad);
  if (!Number.isFinite(oriented.width) || !Number.isFinite(oriented.height)) return undefined;
  if (oriented.width <= 0 || oriented.height <= 0) return undefined;
  const aspect = oriented.width / oriented.height;
  if (!Number.isFinite(aspect) || aspect <= 0) return undefined;
  const clampedAspect = Math.min(settings.maxAspect, Math.max(settings.minAspect, aspect));
  const longEdge = clampInteger(
    Math.round(oriented.width),
    settings.minimumOutputEdge,
    settings.maxDimension,
  );
  let width = longEdge;
  let height = longEdge / clampedAspect;
  if (height < settings.minimumOutputEdge) {
    height = settings.minimumOutputEdge;
    width = Math.min(settings.maxDimension, Math.round(height * clampedAspect));
  }
  width = Math.round(width);
  height = Math.round(height);
  const area = width * height;
  if (area > settings.maxArea) {
    const factor = Math.sqrt(settings.maxArea / area);
    width = Math.max(settings.minimumOutputEdge, Math.floor(width * factor));
    height = Math.max(settings.minimumOutputEdge, Math.floor(height * factor));
  }
  if (width > settings.maxDimension || height > settings.maxDimension) {
    const factor = settings.maxDimension / Math.max(width, height);
    width = Math.max(settings.minimumOutputEdge, Math.floor(width * factor));
    height = Math.max(settings.minimumOutputEdge, Math.floor(height * factor));
  }
  return { width, height, quad: oriented.quad };
}

export function computeDocumentHomography(
  quad: DocumentQuad,
  width: number,
  height: number,
): DocumentHomography | undefined {
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) {
    return undefined;
  }
  const correspondences: readonly (readonly [number, number, Point])[] = [
    [0, 0, quad.topLeft],
    [width, 0, quad.topRight],
    [width, height, quad.bottomRight],
    [0, height, quad.bottomLeft],
  ];
  const matrix: number[][] = [];
  const vector: number[] = [];
  for (const [x, y, point] of correspondences) {
    matrix.push([x, y, 1, 0, 0, 0, -x * point.x, -y * point.x]);
    vector.push(point.x);
    matrix.push([0, 0, 0, x, y, 1, -x * point.y, -y * point.y]);
    vector.push(point.y);
  }
  const solution = solveLinearSystem(matrix, vector);
  if (solution === undefined) return undefined;
  return [...solution, 1];
}

export function applyDocumentHomography(
  homography: DocumentHomography,
  x: number,
  y: number,
): Point | undefined {
  const denominator = homography[6]! * x + homography[7]! * y + homography[8]!;
  if (Math.abs(denominator) < 1e-9) return undefined;
  const mappedX = (homography[0]! * x + homography[1]! * y + homography[2]!) / denominator;
  const mappedY = (homography[3]! * x + homography[4]! * y + homography[5]!) / denominator;
  if (!Number.isFinite(mappedX) || !Number.isFinite(mappedY)) return undefined;
  return { x: mappedX, y: mappedY };
}

export function warpDocumentFrame(
  source: PixelBuffer,
  quad: DocumentQuad,
  options: Partial<DocumentCorrectionOptions> = {},
): CorrectedDocumentFrame | undefined {
  const settings = normalizeDocumentCorrectionOptions(options);
  const size = documentCorrectionSize(quad, settings);
  if (size === undefined) return undefined;
  const homography = computeDocumentHomography(size.quad, size.width, size.height);
  if (homography === undefined) return undefined;
  const { data, width: sourceWidth, height: sourceHeight } = source;
  if (
    sourceWidth <= 0 ||
    sourceHeight <= 0 ||
    data.length < sourceWidth * sourceHeight * 4 ||
    size.width <= 0 ||
    size.height <= 0
  ) {
    return undefined;
  }
  const output = new Uint8ClampedArray(size.width * size.height * 4);
  const background = settings.background;
  for (let index = 0; index < output.length; index += 4) {
    output[index] = background;
    output[index + 1] = background;
    output[index + 2] = background;
    output[index + 3] = 255;
  }
  for (let y = 0; y < size.height; y += 1) {
    const destinationY = y + 0.5;
    for (let x = 0; x < size.width; x += 1) {
      const point = applyDocumentHomography(homography, x + 0.5, destinationY);
      if (point === undefined) continue;
      if (
        point.x < -0.5 ||
        point.y < -0.5 ||
        point.x > sourceWidth - 0.5 ||
        point.y > sourceHeight - 0.5
      ) {
        continue;
      }
      const outputOffset = (y * size.width + x) * 4;
      bilinearSample(data, sourceWidth, sourceHeight, point.x, point.y, output, outputOffset);
    }
  }
  return {
    image: { data: output, width: size.width, height: size.height },
    quad: size.quad,
    width: size.width,
    height: size.height,
  };
}

export function correctDocumentFrame(
  source: PixelBuffer,
  detection: DocumentDetection,
  options: Partial<DocumentCorrectionOptions> = {},
): CorrectedDocumentFrame | undefined {
  if (detection.frameWidth <= 0 || detection.frameHeight <= 0) return undefined;
  const scaleX = source.width / detection.frameWidth;
  const scaleY = source.height / detection.frameHeight;
  if (!Number.isFinite(scaleX) || !Number.isFinite(scaleY) || scaleX <= 0 || scaleY <= 0) {
    return undefined;
  }
  return warpDocumentFrame(source, scaleDocumentQuad(detection.quad, scaleX, scaleY), options);
}

function bilinearSample(
  source: Uint8ClampedArray | Uint8Array,
  width: number,
  height: number,
  x: number,
  y: number,
  output: Uint8ClampedArray,
  outputOffset: number,
): void {
  const x0 = Math.floor(x);
  const y0 = Math.floor(y);
  const left = Math.max(0, Math.min(width - 1, x0));
  const top = Math.max(0, Math.min(height - 1, y0));
  const right = Math.max(0, Math.min(width - 1, x0 + 1));
  const bottom = Math.max(0, Math.min(height - 1, y0 + 1));
  const fractionX = Math.max(0, Math.min(1, x - x0));
  const fractionY = Math.max(0, Math.min(1, y - y0));
  const topLeft = (top * width + left) * 4;
  const topRight = (top * width + right) * 4;
  const bottomLeft = (bottom * width + left) * 4;
  const bottomRight = (bottom * width + right) * 4;
  const topLeftWeight = (1 - fractionX) * (1 - fractionY);
  const topRightWeight = fractionX * (1 - fractionY);
  const bottomLeftWeight = (1 - fractionX) * fractionY;
  const bottomRightWeight = fractionX * fractionY;
  for (let channel = 0; channel < 3; channel += 1) {
    output[outputOffset + channel] = Math.round(
      source[topLeft + channel]! * topLeftWeight +
        source[topRight + channel]! * topRightWeight +
        source[bottomLeft + channel]! * bottomLeftWeight +
        source[bottomRight + channel]! * bottomRightWeight,
    );
  }
}

function solveLinearSystem(matrix: number[][], vector: number[]): number[] | undefined {
  const size = vector.length;
  for (let column = 0; column < size; column += 1) {
    let pivot = column;
    for (let row = column + 1; row < size; row += 1) {
      if (Math.abs(matrix[row]![column]!) > Math.abs(matrix[pivot]![column]!)) {
        pivot = row;
      }
    }
    if (Math.abs(matrix[pivot]![column]!) < 1e-12) return undefined;
    if (pivot !== column) {
      [matrix[pivot], matrix[column]] = [matrix[column]!, matrix[pivot]!];
      [vector[pivot], vector[column]] = [vector[column]!, vector[pivot]!];
    }
    for (let row = column + 1; row < size; row += 1) {
      const factor = matrix[row]![column]! / matrix[column]![column]!;
      if (factor === 0) continue;
      for (let entry = column; entry < size; entry += 1) {
        matrix[row]![entry] = matrix[row]![entry]! - factor * matrix[column]![entry]!;
      }
      vector[row] = vector[row]! - factor * vector[column]!;
    }
  }
  const solution = new Array<number>(size);
  for (let row = size - 1; row >= 0; row -= 1) {
    let sum = vector[row]!;
    for (let column = row + 1; column < size; column += 1) {
      sum -= matrix[row]![column]! * solution[column]!;
    }
    solution[row] = sum / matrix[row]![row]!;
  }
  if (solution.some((value) => !Number.isFinite(value))) return undefined;
  return solution;
}

function clampInteger(value: number, minimum: number, maximum: number): number {
  return Math.min(maximum, Math.max(minimum, Math.round(value)));
}

function boundedInteger(
  value: number | undefined,
  name: string,
  minimum: number,
  maximum: number,
  fallback: number,
): number {
  if (value === undefined) return fallback;
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) {
    throw new TypeError(`${name} must be an integer between ${minimum} and ${maximum}.`);
  }
  return value;
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
