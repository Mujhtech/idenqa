/** Opaque sRGB colours used by the Capture Web text, controls and focus ring. */
export interface CaptureThemePalette {
  readonly background: string;
  readonly surface: string;
  readonly surfaceStrong: string;
  readonly text: string;
  readonly muted: string;
  readonly accent: string;
  readonly accentStrong: string;
  readonly accentForeground: string;
}

export interface CaptureThemeContrastIssue {
  readonly foreground: keyof CaptureThemePalette;
  readonly background: keyof CaptureThemePalette;
  readonly minimum: number;
  /** Absent when either colour cannot be evaluated as opaque sRGB. */
  readonly ratio?: number;
  readonly reason: "insufficient_contrast" | "unsupported_colour";
}

/** WCAG 2.x relative-luminance ratio. Unsupported/transparent colours return undefined. */
export function captureContrastRatio(foreground: string, background: string): number | undefined {
  const first = luminance(foreground);
  const second = luminance(background);
  if (first === undefined || second === undefined) return undefined;
  return (Math.max(first, second) + 0.05) / (Math.min(first, second) + 0.05);
}

/**
 * Check a resolved palette, including hover, secondary text, and focus colours.
 * Text uses 4.5:1 even when a particular heading could qualify as large text;
 * the offset focus ring uses 3:1 against its surrounding surface. This is an
 * authoring/conformance diagnostic, not a claim of whole-page accessibility.
 */
export function auditCaptureThemeContrast(
  palette: CaptureThemePalette,
): readonly CaptureThemeContrastIssue[] {
  const pairs: readonly [keyof CaptureThemePalette, keyof CaptureThemePalette, number][] = [
    ["text", "background", 4.5],
    ["text", "surface", 4.5],
    ["text", "surfaceStrong", 4.5],
    ["muted", "background", 4.5],
    ["muted", "surface", 4.5],
    ["accentForeground", "accent", 4.5],
    ["accentForeground", "accentStrong", 4.5],
    ["accentStrong", "surfaceStrong", 4.5],
    ["accent", "background", 3],
    ["accent", "surface", 3],
  ];
  return pairs.flatMap<CaptureThemeContrastIssue>(([foreground, background, minimum]) => {
    const ratio = captureContrastRatio(palette[foreground], palette[background]);
    if (ratio === undefined)
      return [{ foreground, background, minimum, reason: "unsupported_colour" as const }];
    return ratio < minimum
      ? [{ foreground, background, minimum, ratio, reason: "insufficient_contrast" as const }]
      : [];
  });
}

function luminance(colour: string): number | undefined {
  const value = colour.trim();
  let channels: number[];
  if (/^#[\da-f]{3}$/i.test(value)) {
    channels = [...value.slice(1)].map((digit) => Number.parseInt(digit + digit, 16));
  } else if (/^#[\da-f]{6}$/i.test(value)) {
    channels = [1, 3, 5].map((offset) => Number.parseInt(value.slice(offset, offset + 2), 16));
  } else {
    // getComputedStyle serialises opaque legacy sRGB colours in this form.
    const match = /^rgb\(\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)\s*\)$/i.exec(value);
    if (match === null) return undefined;
    channels = match.slice(1).map(Number);
    if (channels.some((channel) => !Number.isFinite(channel) || channel < 0 || channel > 255))
      return undefined;
  }
  const linear = channels.map((channel) => {
    const normal = channel / 255;
    return normal <= 0.04045 ? normal / 12.92 : ((normal + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * linear[0]! + 0.7152 * linear[1]! + 0.0722 * linear[2]!;
}
