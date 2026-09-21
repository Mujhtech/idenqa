import type { ExperienceLinks, ExperiencePinned, ExperienceResolution } from "@idenqa/sdk";

import type { CaptureMessageCatalogue, CaptureMessageKey } from "./localisation.js";

/**
 * Portable-experience presentation derived from a signed bootstrap resolution.
 * Tenant copy overlays only the closed allow-listed keys below; Core-owned
 * mandatory copy, notices, assurance, and capture semantics are never changed.
 */
export interface CaptureExperiencePresentation {
  readonly locale?: string;
  readonly messageCatalogue?: CaptureMessageCatalogue;
  readonly theme?: Readonly<Record<string, string>>;
  readonly links?: ExperienceLinks;
  readonly fallback: boolean;
  readonly pinned?: ExperiencePinned;
  readonly mandatoryCopy?: Readonly<Record<string, string>>;
}

/**
 * Closed mapping from portable experience copy keys to package-owned UI
 * message keys. Unknown tenant keys are ignored so a tenant document cannot
 * rewrite safety or notice semantics.
 */
const EXPERIENCE_COPY_MESSAGES: Readonly<Record<string, readonly CaptureMessageKey[]>> = {
  "ui.intro.title": ["introTitle"],
  "ui.intro.body": ["introBody"],
  "ui.intro.private": ["introPrivate"],
  "ui.intro.device": ["introDevice"],
  "ui.get_started": ["getStarted"],
  "ui.capture.title": ["captureTitle"],
  "ui.capture.camera": ["cameraInstruction"],
  "ui.capture.file": ["fileInstruction"],
  "ui.capture.other": ["otherInstruction"],
  "ui.tip.lighting": ["tipLighting"],
  "ui.tip.readable": ["tipReadable"],
  "ui.tip.privacy": ["tipPrivacy"],
  "ui.continue": ["continue"],
  "ui.back": ["back"],
  "ui.cancel": ["cancel"],
  "ui.choose_method": ["chooseMethod"],
  "ui.choose_method.title": ["chooseMethodTitle"],
  "ui.choose_method.body": ["chooseMethodBody"],
  "ui.prepare": ["prepare"],
  "ui.prepare.title": ["prepareTitle"],
  "ui.processing.title": ["processingTitle"],
  "ui.processing.body": ["processingBody"],
  "ui.complete": ["captureComplete"],
};

const THEME_TOKENS: Readonly<Record<string, string>> = {
  primary_color: "--idq-capture-accent",
  accent_color: "--idq-capture-accent-strong",
  background_color: "--idq-capture-background",
  text_color: "--idq-capture-text",
};

/** Derive bounded presentation from a signed experience resolution. */
export function captureExperiencePresentation(
  resolution: ExperienceResolution,
): CaptureExperiencePresentation {
  const document = resolution.manifest.document;
  const locale = resolution.pinned.locale;
  const localeCopy = document.copy.locales.find((entry) => entry.locale === locale);
  const messages: Partial<Record<CaptureMessageKey, string>> = {};
  for (const entry of localeCopy?.entries ?? []) {
    const keys = EXPERIENCE_COPY_MESSAGES[entry.key];
    if (keys === undefined) continue;
    for (const key of keys) messages[key] = entry.value;
  }
  const theme: Record<string, string> = {};
  for (const [field, token] of Object.entries(THEME_TOKENS)) {
    const value = document.theme[field as keyof typeof document.theme];
    if (typeof value === "string") theme[token] = value;
  }
  const mandatoryCopy: Record<string, string> = {};
  for (const entry of resolution.mandatory_copy.entries) mandatoryCopy[entry.key] = entry.value;
  return {
    locale,
    theme,
    links: document.links,
    fallback: resolution.fallback,
    pinned: resolution.pinned,
    mandatoryCopy,
    ...(Object.keys(messages).length === 0 ? {} : { messageCatalogue: { [locale]: messages } }),
  };
}

/**
 * Apply safe theme tokens only where the host has not provided an
 * appearance-only `--idq-capture-*` value. Host branding always wins.
 */
export function applyCaptureExperienceTheme(
  element: HTMLElement,
  theme: Readonly<Record<string, string>> | undefined,
): void {
  if (theme === undefined) return;
  const computed = getComputedStyle(element);
  for (const [token, value] of Object.entries(theme)) {
    if (computed.getPropertyValue(token).trim() !== "") continue;
    element.style.setProperty(token, value);
  }
}
