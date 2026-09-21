import { describe, expect, it } from "vitest";

import {
  applyDocumentHomography,
  computeDocumentHomography,
  correctDocumentFrame,
  documentCorrectionSize,
  normalizeDocumentCorrectionOptions,
  orientDocumentQuad,
  warpDocumentFrame,
  type DocumentDetection,
  type DocumentQuad,
  type PixelBuffer,
  type Point,
} from "../src/index.js";

const LANDSCAPE_QUAD: DocumentQuad = {
  topLeft: { x: 60, y: 40 },
  topRight: { x: 280, y: 50 },
  bottomRight: { x: 270, y: 210 },
  bottomLeft: { x: 50, y: 200 },
};

describe("documentCorrectionSize", () => {
  it("preserves a document aspect within the configured clamp", () => {
    const size = documentCorrectionSize(LANDSCAPE_QUAD);
    expect(size).toBeDefined();
    expect(size!.width).toBeGreaterThan(200);
    expect(size!.width).toBeLessThan(240);
    expect(size!.width / size!.height).toBeGreaterThan(1.3);
    expect(size!.width / size!.height).toBeLessThan(1.45);
  });

  it("normalises a portrait detection to a landscape rectangle", () => {
    const portrait: DocumentQuad = {
      topLeft: { x: 100, y: 20 },
      topRight: { x: 180, y: 22 },
      bottomRight: { x: 178, y: 320 },
      bottomLeft: { x: 98, y: 318 },
    };
    const oriented = orientDocumentQuad(portrait);
    expect(oriented.width).toBeGreaterThan(oriented.height);
    const size = documentCorrectionSize(portrait);
    expect(size!.width).toBeGreaterThan(size!.height);
  });

  it("clamps extreme aspects to sensible document bounds", () => {
    const wide = documentCorrectionSize({
      topLeft: { x: 0, y: 0 },
      topRight: { x: 1000, y: 0 },
      bottomRight: { x: 1000, y: 100 },
      bottomLeft: { x: 0, y: 100 },
    });
    expect(wide!.width / wide!.height).toBeCloseTo(1.9, 2);

    const tall = documentCorrectionSize({
      topLeft: { x: 0, y: 0 },
      topRight: { x: 100, y: 0 },
      bottomRight: { x: 100, y: 1000 },
      bottomLeft: { x: 0, y: 1000 },
    });
    expect(tall!.width / tall!.height).toBeCloseTo(1.9, 2);
  });

  it("bounds the longest edge and total area", () => {
    const large = documentCorrectionSize(
      {
        topLeft: { x: 0, y: 0 },
        topRight: { x: 5000, y: 0 },
        bottomRight: { x: 5000, y: 3000 },
        bottomLeft: { x: 0, y: 3000 },
      },
      { maxDimension: 2048 },
    );
    expect(Math.max(large!.width, large!.height)).toBeLessThanOrEqual(2048);

    const smallArea = documentCorrectionSize(LANDSCAPE_QUAD, {
      maxArea: 65_536,
      maxDimension: 2048,
    });
    expect(smallArea!.width * smallArea!.height).toBeLessThanOrEqual(65_536 + 2048);
  });

  it("rejects degenerate quads and invalid option combinations", () => {
    expect(
      documentCorrectionSize({
        topLeft: { x: 0, y: 0 },
        topRight: { x: 0, y: 0 },
        bottomRight: { x: 0, y: 0 },
        bottomLeft: { x: 0, y: 0 },
      }),
    ).toBeUndefined();
    expect(() => normalizeDocumentCorrectionOptions({ minAspect: 1.8, maxAspect: 1.5 })).toThrow(
      TypeError,
    );
  });
});

describe("computeDocumentHomography", () => {
  it("maps the output rectangle onto the detected quad", () => {
    const homography = computeDocumentHomography(LANDSCAPE_QUAD, 300, 200);
    expect(homography).toBeDefined();
    expectPointClose(applyDocumentHomography(homography!, 0, 0)!, LANDSCAPE_QUAD.topLeft);
    expectPointClose(applyDocumentHomography(homography!, 300, 0)!, LANDSCAPE_QUAD.topRight);
    expectPointClose(applyDocumentHomography(homography!, 300, 200)!, LANDSCAPE_QUAD.bottomRight);
    expectPointClose(applyDocumentHomography(homography!, 0, 200)!, LANDSCAPE_QUAD.bottomLeft);
  });

  it("returns undefined for degenerate quads", () => {
    expect(
      computeDocumentHomography(
        {
          topLeft: { x: 0, y: 0 },
          topRight: { x: 10, y: 10 },
          bottomRight: { x: 20, y: 20 },
          bottomLeft: { x: 30, y: 30 },
        },
        100,
        80,
      ),
    ).toBeUndefined();
    expect(computeDocumentHomography(LANDSCAPE_QUAD, 0, 0)).toBeUndefined();
  });
});

describe("warpDocumentFrame", () => {
  it("straightens a perspective quad and preserves the fill", () => {
    const source = filledSource(320, 240, 0);
    paintPolygon(source, quadPoints(LANDSCAPE_QUAD), 210);

    const corrected = warpDocumentFrame(source, LANDSCAPE_QUAD);
    expect(corrected).toBeDefined();
    const size = documentCorrectionSize(LANDSCAPE_QUAD)!;
    expect(corrected!.width).toBe(size.width);
    expect(corrected!.height).toBe(size.height);
    for (let y = 2; y < corrected!.height - 2; y += 1) {
      for (let x = 2; x < corrected!.width - 2; x += 1) {
        const offset = (y * corrected!.width + x) * 4;
        expect(corrected!.image.data[offset]).toBe(210);
        expect(corrected!.image.data[offset + 3]).toBe(255);
      }
    }
  });

  it("fills regions that map outside the source with the background", () => {
    const source = filledSource(100, 100, 0);
    const outside: DocumentQuad = {
      topLeft: { x: -30, y: 10 },
      topRight: { x: 130, y: 10 },
      bottomRight: { x: 130, y: 90 },
      bottomLeft: { x: -30, y: 90 },
    };
    const corrected = warpDocumentFrame(source, outside);
    expect(corrected).toBeDefined();
    const background = corrected!.image.data[0]!;
    expect(background).toBe(255);
    const middle =
      (Math.floor(corrected!.height / 2) * corrected!.width + Math.floor(corrected!.width / 2)) * 4;
    expect(corrected!.image.data[middle]).toBe(0);
  });

  it("returns undefined instead of writing an invalid image for degenerate input", () => {
    const source = filledSource(100, 100, 0);
    expect(
      warpDocumentFrame(source, {
        topLeft: { x: 10, y: 10 },
        topRight: { x: 20, y: 20 },
        bottomRight: { x: 30, y: 30 },
        bottomLeft: { x: 40, y: 40 },
      }),
    ).toBeUndefined();
    expect(warpDocumentFrame(filledSource(0, 0, 0), LANDSCAPE_QUAD)).toBeUndefined();
  });

  it("maps a low-resolution uniform source to the requested bounds", () => {
    const source = filledSource(80, 80, 120);
    const quad: DocumentQuad = {
      topLeft: { x: 10, y: 10 },
      topRight: { x: 70, y: 10 },
      bottomRight: { x: 70, y: 60 },
      bottomLeft: { x: 10, y: 60 },
    };
    const corrected = warpDocumentFrame(source, quad);
    expect(corrected).toBeDefined();
    expect(corrected!.width).toBe(77);
    expect(corrected!.height).toBe(64);
    const middle = Math.floor(corrected!.height / 2) * corrected!.width * 4;
    expect(corrected!.image.data[middle]).toBe(120);
  });
});

describe("correctDocumentFrame", () => {
  it("scales an observation-space detection to the full-resolution source", () => {
    const source = filledSource(640, 480, 0);
    const quad: DocumentQuad = {
      topLeft: { x: 60, y: 40 },
      topRight: { x: 280, y: 50 },
      bottomRight: { x: 270, y: 210 },
      bottomLeft: { x: 50, y: 200 },
    };
    const scaledQuad: DocumentQuad = {
      topLeft: { x: 120, y: 80 },
      topRight: { x: 560, y: 100 },
      bottomRight: { x: 540, y: 420 },
      bottomLeft: { x: 100, y: 400 },
    };
    paintPolygon(source, quadPoints(scaledQuad), 200);
    const detection: DocumentDetection = {
      quad,
      frameWidth: 320,
      frameHeight: 240,
      coverage: 0.45,
      aspectRatio: 1.5,
      confidence: 0.8,
    };

    const corrected = correctDocumentFrame(source, detection);
    expect(corrected).toBeDefined();
    expect(corrected!.width).toBeGreaterThan(400);
    const middle =
      (Math.floor(corrected!.height / 2) * corrected!.width + Math.floor(corrected!.width / 2)) * 4;
    expect(corrected!.image.data[middle]).toBe(200);
  });

  it("returns undefined when the detection frame size is invalid", () => {
    expect(
      correctDocumentFrame(filledSource(10, 10, 0), {
        quad: LANDSCAPE_QUAD,
        frameWidth: 0,
        frameHeight: 0,
        coverage: 0.5,
        aspectRatio: 1.5,
        confidence: 0.8,
      }),
    ).toBeUndefined();
  });
});

function filledSource(width: number, height: number, fill: number): PixelBuffer {
  const data = new Uint8ClampedArray(width * height * 4);
  for (let index = 0; index < data.length; index += 4) {
    data[index] = fill;
    data[index + 1] = fill;
    data[index + 2] = fill;
    data[index + 3] = 255;
  }
  return { data, width, height };
}

function quadPoints(quad: DocumentQuad): readonly Point[] {
  return [quad.topLeft, quad.topRight, quad.bottomRight, quad.bottomLeft];
}

function paintPolygon(source: PixelBuffer, corners: readonly Point[], value: number): void {
  const minimumY = Math.max(0, Math.floor(Math.min(...corners.map((point) => point.y))));
  const maximumY = Math.min(
    source.height - 1,
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
      const to = Math.min(source.width - 1, Math.floor(intersections[index + 1]!));
      for (let x = from; x <= to; x += 1) {
        const offset = (y * source.width + x) * 4;
        source.data[offset] = value;
        source.data[offset + 1] = value;
        source.data[offset + 2] = value;
        source.data[offset + 3] = 255;
      }
    }
  }
}

function expectPointClose(actual: Point, expected: Point): void {
  expect(actual.x).toBeCloseTo(expected.x, 6);
  expect(actual.y).toBeCloseTo(expected.y, 6);
}
