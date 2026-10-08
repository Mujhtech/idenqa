import type { CaptureActiveFlowSnapshot } from "../flow.js";
import type { CaptureCountryJourneyNotice, CaptureCountryOption } from "./types.js";

export function normalizeCountryOptions(
  countries: readonly CaptureCountryOption[],
): readonly CaptureCountryOption[] {
  if (countries.length === 0 || countries.length > 249) {
    throw new TypeError("Country selection requires between 1 and 249 approved countries.");
  }
  const seen = new Set<string>();
  return countries.map((country) => {
    const code = country.code.trim().toUpperCase();
    const label = country.label.trim();
    if (!/^[A-Z]{2}$/.test(code) || label.length === 0 || label.length > 100) {
      throw new TypeError("Country options require an ISO alpha-2 code and a display label.");
    }
    if (seen.has(code)) throw new TypeError(`Country option ${code} is duplicated.`);
    seen.add(code);
    return { code, label };
  });
}

export function countryJourneyNoticeMatches(
  expected: CaptureCountryJourneyNotice,
  snapshot: CaptureActiveFlowSnapshot,
): boolean {
  const { notice, authority } = snapshot.authoritySnapshot;
  return (
    authority.consentRequired === expected.consentRequired &&
    notice.locale === expected.locale &&
    notice.controller === expected.controller &&
    notice.recipient === expected.recipient &&
    notice.copy.title === expected.copy.title &&
    notice.copy.summary === expected.copy.summary &&
    notice.copy.purpose === expected.copy.purpose &&
    notice.copy.consequences === expected.copy.consequences
  );
}
