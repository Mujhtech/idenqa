/**
 * Portable capture-experience v1 contract, mirroring
 * `contracts/experience/v1`. The validator, canonical encoder, digest, and
 * manifest verification are dependency-free and produce byte-identical
 * canonical bytes to the Go implementation.
 */

export type ExperienceID = string;

export interface ExperienceCopyEntry {
  readonly key: string;
  readonly value: string;
}

export interface ExperienceLocaleCopy {
  readonly locale: string;
  readonly entries: readonly ExperienceCopyEntry[];
}

export interface ExperienceCopy {
  readonly version: string;
  readonly locales: readonly ExperienceLocaleCopy[];
}

export interface ExperienceTarget {
  readonly workflow?: string;
  readonly countries?: readonly string[];
  readonly application_ids?: readonly string[];
  readonly origins?: readonly string[];
  readonly sdk_version_min?: string;
  readonly sdk_version_max?: string;
}

export type ExperienceAssetMIME = "image/png" | "image/jpeg" | "image/webp" | "image/avif";

export interface ExperienceAsset {
  readonly key: string;
  readonly kind: "image";
  readonly mime: ExperienceAssetMIME;
  readonly size: number;
  readonly digest: string;
  readonly object_key: string;
  readonly object_version: string;
}

export interface ExperienceCustomLink {
  readonly label: string;
  readonly url: string;
}

export interface ExperienceLinks {
  readonly support: string;
  readonly privacy: string;
  readonly terms: string;
  readonly custom?: readonly ExperienceCustomLink[];
}

export interface ExperienceTheme {
  readonly primary_color: string;
  readonly accent_color: string;
  readonly background_color: string;
  readonly text_color: string;
  readonly logo_asset_key?: string;
}

export interface ExperienceDocument {
  readonly schema_version: "1.0";
  readonly experience_id: ExperienceID;
  readonly version: number;
  readonly name: string;
  readonly copy: ExperienceCopy;
  readonly mandatory_copy_version: string;
  readonly default_locale: string;
  readonly targeting: readonly ExperienceTarget[];
  readonly assets?: readonly ExperienceAsset[];
  readonly links: ExperienceLinks;
  readonly theme: ExperienceTheme;
  readonly allowed_origins?: readonly string[];
}

export interface ExperienceManifest {
  readonly document: ExperienceDocument;
  readonly digest: string;
  readonly key_id: string;
  readonly algorithm: "ed25519";
  readonly signature: string;
}

export interface ExperienceMandatoryCopy {
  readonly version: string;
  readonly digest: string;
  readonly entries: readonly ExperienceCopyEntry[];
}

export interface ExperienceRequestOptions {
  readonly workflow?: string;
  readonly country?: string;
  readonly applicationId?: string;
  readonly origin?: string;
  readonly sdkVersion?: string;
  readonly locale?: string;
  readonly signal?: AbortSignal;
}

export type ExperiencePinSource = "pinned" | "published" | "default";

export interface ExperiencePinned {
  readonly experience_id: ExperienceID;
  readonly version: number;
  readonly locale: string;
  readonly tenant_copy_version: string;
  readonly mandatory_copy_version: string;
  readonly source: ExperiencePinSource;
  readonly digest: string;
  readonly key_id: string;
  readonly pinned_at: string;
}

export interface ExperienceResolution {
  readonly manifest: ExperienceManifest;
  readonly mandatory_copy: ExperienceMandatoryCopy;
  readonly pinned: ExperiencePinned;
  readonly fallback: boolean;
}

export const EXPERIENCE_SCHEMA_VERSION = "1.0" as const;
export const EXPERIENCE_SIGNATURE_ALGORITHM = "ed25519" as const;

export const EXPERIENCE_LIMITS = {
  documentBytes: 256 * 1024,
  locales: 16,
  copyEntriesPerLocale: 64,
  copyValueBytes: 2048,
  copyKeyBytes: 128,
  assets: 16,
  assetBytes: 2 * 1024 * 1024,
  customLinks: 8,
  allowedOrigins: 16,
  targetingRules: 32,
  nameBytes: 120,
  urlBytes: 2048,
} as const;

const RESERVED_COPY_PREFIXES = ["regulatory.", "consent.", "safety.", "accessibility."] as const;
const ALLOWED_MIMES: readonly string[] = ["image/png", "image/jpeg", "image/webp", "image/avif"];
const LOCALE_PATTERN = /^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$/;
const COPY_KEY_PATTERN = /^[a-z][a-z0-9._-]{0,127}$/;
const COPY_VERSION_PATTERN = /^[a-z0-9][a-z0-9._-]{0,63}$/;
const EXPERIENCE_ID_PATTERN = /^exp_[0-9A-HJKMNP-TV-Z]{26}$/;
const MANDATORY_VERSION_PATTERN = /^mc-[0-9]{4}-[0-9]{2}-[0-9]{2}(-[0-9]{1,2})?$/;
const DIGEST_PATTERN = /^[0-9a-f]{64}$/;
const KEY_ID_PATTERN = /^[a-z][a-z0-9._-]{0,63}$/;
const ASSET_KEY_PATTERN = /^[a-z][a-z0-9._-]{0,63}$/;
const OBJECT_KEY_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._/-]{0,511}$/;
const OBJECT_VERSION_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$/;
const COLOR_PATTERN = /^#[0-9a-f]{6}$/;
const COUNTRY_PATTERN = /^[A-Z]{2}$/;
const WORKFLOW_PATTERN = /^[a-z][a-z0-9._-]{0,127}$/;
const SEMVER_PATTERN = /^[0-9]+\.[0-9]+\.[0-9]+$/;

/** ExperienceContractError identifies a closed-contract violation. */
export class ExperienceContractError extends Error {
  readonly code: string;

  constructor(message: string, code = "EXPERIENCE_INVALID") {
    super(message);
    this.name = "ExperienceContractError";
    this.code = code;
  }
}

function fail(field: string, code = "EXPERIENCE_INVALID"): never {
  throw new ExperienceContractError(`experience v1: ${field}`, code);
}

function assertObject(value: unknown, field: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) fail(field);
  return value as Record<string, unknown>;
}

function assertArray(value: unknown, field: string, maximum: number): unknown[] {
  if (!Array.isArray(value) || value.length > maximum) fail(field);
  return value;
}

function assertString(value: unknown, field: string, maximum: number, minimum = 1): string {
  if (typeof value !== "string" || value.length < minimum || byteLength(value) > maximum) {
    fail(field);
  }
  return value;
}

function assertClosed(
  value: Record<string, unknown>,
  field: string,
  allowed: readonly string[],
): void {
  for (const key of Object.keys(value)) {
    if (!allowed.includes(key)) fail(`${field}.${key}`);
  }
}

function byteLength(value: string): number {
  return new TextEncoder().encode(value).length;
}

function assertText(value: unknown, field: string, maximum: number): string {
  const text = assertString(value, field, maximum);
  for (const character of text) {
    const codePoint = character.codePointAt(0) ?? 0;
    if (codePoint < 0x20 && character !== "\n" && character !== "\t") fail(field);
  }
  return text;
}

function assertUnique(values: readonly string[], field: string): void {
  if (new Set(values).size !== values.length) fail(field);
}

function validateHTTPSURL(value: unknown, field: string): string {
  const text = assertString(value, field, EXPERIENCE_LIMITS.urlBytes);
  let parsed: URL;
  try {
    parsed = new URL(text);
  } catch {
    return fail(field);
  }
  if (
    parsed.protocol !== "https:" ||
    parsed.host === "" ||
    parsed.username !== "" ||
    parsed.hash !== ""
  ) {
    fail(field);
  }
  return text;
}

function validateOrigin(value: unknown, field: string): string {
  const text = assertString(value, field, EXPERIENCE_LIMITS.urlBytes);
  let parsed: URL;
  try {
    parsed = new URL(text);
  } catch {
    return fail(field);
  }
  if (
    parsed.protocol !== "https:" ||
    parsed.host === "" ||
    parsed.username !== "" ||
    parsed.search !== "" ||
    parsed.hash !== "" ||
    (parsed.pathname !== "" && parsed.pathname !== "/") ||
    parsed.host !== parsed.host.toLowerCase()
  ) {
    fail(field);
  }
  return text;
}

function validateCopy(value: unknown): void {
  const copy = assertObject(value, "copy");
  assertClosed(copy, "copy", ["version", "locales"]);
  if (typeof copy.version !== "string" || !COPY_VERSION_PATTERN.test(copy.version))
    fail("copy.version");
  const locales = assertArray(copy.locales, "copy.locales", EXPERIENCE_LIMITS.locales);
  if (locales.length === 0) fail("copy.locales");
  const seenLocales = new Set<string>();
  for (const [index, localeValue] of locales.entries()) {
    const locale = assertObject(localeValue, `copy.locales[${index}]`);
    assertClosed(locale, `copy.locales[${index}]`, ["locale", "entries"]);
    const tag = assertString(locale.locale, `copy.locales[${index}].locale`, 35);
    if (!LOCALE_PATTERN.test(tag)) fail(`copy.locales[${index}].locale`);
    if (seenLocales.has(tag)) fail(`copy.locales[${index}].locale duplicate`);
    seenLocales.add(tag);
    const entries = assertArray(
      locale.entries,
      `copy.locales[${index}].entries`,
      EXPERIENCE_LIMITS.copyEntriesPerLocale,
    );
    if (entries.length === 0) fail(`copy.locales[${index}].entries`);
    const seenKeys = new Set<string>();
    for (const [entryIndex, entryValue] of entries.entries()) {
      const entry = assertObject(entryValue, `copy.locales[${index}].entries[${entryIndex}]`);
      assertClosed(entry, `copy.locales[${index}].entries[${entryIndex}]`, ["key", "value"]);
      const key = assertString(
        entry.key,
        `copy.locales[${index}].entries[${entryIndex}].key`,
        EXPERIENCE_LIMITS.copyKeyBytes,
      );
      if (!COPY_KEY_PATTERN.test(key)) fail(`copy.locales[${index}].entries[${entryIndex}].key`);
      if (RESERVED_COPY_PREFIXES.some((prefix) => key.startsWith(prefix))) {
        fail(`copy.locales[${index}].entries[${entryIndex}].key`, "EXPERIENCE_RESERVED_COPY");
      }
      if (seenKeys.has(key)) fail(`copy.locales[${index}].entries[${entryIndex}].key duplicate`);
      seenKeys.add(key);
      assertText(
        entry.value,
        `copy.locales[${index}].entries[${entryIndex}].value`,
        EXPERIENCE_LIMITS.copyValueBytes,
      );
    }
  }
}

function compareSemver(left: string, right: string): number {
  const leftParts = left.split(".").map(Number);
  const rightParts = right.split(".").map(Number);
  for (let index = 0; index < 3; index += 1) {
    const leftValue = leftParts[index] ?? 0;
    const rightValue = rightParts[index] ?? 0;
    if (leftValue !== rightValue) return leftValue < rightValue ? -1 : 1;
  }
  return 0;
}

function validateTargeting(value: unknown): void {
  const rules = assertArray(value, "targeting", EXPERIENCE_LIMITS.targetingRules);
  const fingerprints = new Set<string>();
  for (const [index, ruleValue] of rules.entries()) {
    const rule = assertObject(ruleValue, `targeting[${index}]`);
    assertClosed(rule, `targeting[${index}]`, [
      "workflow",
      "countries",
      "application_ids",
      "origins",
      "sdk_version_min",
      "sdk_version_max",
    ]);
    const countries = optionalStringArray(rule.countries, `targeting[${index}].countries`, 64);
    for (const country of countries) {
      if (!COUNTRY_PATTERN.test(country)) fail(`targeting[${index}].countries`);
    }
    const applications = optionalStringArray(
      rule.application_ids,
      `targeting[${index}].application_ids`,
      16,
    );
    for (const application of applications)
      assertString(application, `targeting[${index}].application_ids`, 200);
    const origins = optionalStringArray(rule.origins, `targeting[${index}].origins`, 16);
    origins.forEach((origin) => validateOrigin(origin, `targeting[${index}].origins`));
    assertUnique(countries, `targeting[${index}].countries`);
    assertUnique(applications, `targeting[${index}].application_ids`);
    assertUnique(origins, `targeting[${index}].origins`);
    if (rule.workflow !== undefined) {
      const workflow = assertString(rule.workflow, `targeting[${index}].workflow`, 128);
      if (!WORKFLOW_PATTERN.test(workflow)) fail(`targeting[${index}].workflow`);
    }
    const minimum = rule.sdk_version_min;
    const maximum = rule.sdk_version_max;
    if ((minimum === undefined) !== (maximum === undefined))
      fail(`targeting[${index}].sdk_version`);
    if (minimum !== undefined && maximum !== undefined) {
      const min = assertString(minimum, `targeting[${index}].sdk_version_min`, 32);
      const max = assertString(maximum, `targeting[${index}].sdk_version_max`, 32);
      if (!SEMVER_PATTERN.test(min) || !SEMVER_PATTERN.test(max) || compareSemver(min, max) > 0) {
        fail(`targeting[${index}].sdk_version`);
      }
    }
    const fingerprint = [
      rule.workflow ?? "",
      countries.join(","),
      applications.join(","),
      origins.join(","),
      minimum ?? "",
      maximum ?? "",
    ].join("\u0000");
    if (fingerprints.has(fingerprint)) fail(`targeting[${index}] duplicate rule`);
    fingerprints.add(fingerprint);
  }
}

function optionalStringArray(value: unknown, field: string, maximum: number): readonly string[] {
  if (value === undefined) return [];
  const values = assertArray(value, field, maximum);
  return values.map((item, index) => {
    if (typeof item !== "string") return fail(`${field}[${index}]`);
    return item;
  });
}

function validateAssets(value: unknown, theme: Record<string, unknown>): void {
  if (value === undefined) {
    if (theme.logo_asset_key !== undefined) fail("theme.logo_asset_key has no asset");
    return;
  }
  const assets = assertArray(value, "assets", EXPERIENCE_LIMITS.assets);
  const seen = new Set<string>();
  for (const [index, assetValue] of assets.entries()) {
    const asset = assertObject(assetValue, `assets[${index}]`);
    assertClosed(asset, `assets[${index}]`, [
      "key",
      "kind",
      "mime",
      "size",
      "digest",
      "object_key",
      "object_version",
    ]);
    const key = assertString(asset.key, `assets[${index}].key`, 64);
    if (!ASSET_KEY_PATTERN.test(key)) fail(`assets[${index}].key`);
    if (seen.has(key)) fail(`assets[${index}].key duplicate`);
    seen.add(key);
    if (asset.kind !== "image") fail(`assets[${index}].kind`);
    if (typeof asset.mime !== "string" || !ALLOWED_MIMES.includes(asset.mime))
      fail(`assets[${index}].mime`);
    if (
      typeof asset.size !== "number" ||
      !Number.isInteger(asset.size) ||
      asset.size < 1 ||
      asset.size > EXPERIENCE_LIMITS.assetBytes
    ) {
      fail(`assets[${index}].size`);
    }
    if (typeof asset.digest !== "string" || !DIGEST_PATTERN.test(asset.digest))
      fail(`assets[${index}].digest`);
    const objectKey = assertString(asset.object_key, `assets[${index}].object_key`, 512);
    if (
      !OBJECT_KEY_PATTERN.test(objectKey) ||
      objectKey.split("/").some((segment) => segment === "" || segment === "." || segment === "..")
    ) {
      fail(`assets[${index}].object_key`);
    }
    const objectVersion = assertString(
      asset.object_version,
      `assets[${index}].object_version`,
      200,
    );
    if (
      !OBJECT_VERSION_PATTERN.test(objectVersion) ||
      objectVersion === "." ||
      objectVersion === ".."
    ) {
      fail(`assets[${index}].object_version`);
    }
  }
  if (theme.logo_asset_key !== undefined) {
    const logo = assertString(theme.logo_asset_key, "theme.logo_asset_key", 64);
    if (!ASSET_KEY_PATTERN.test(logo)) fail("theme.logo_asset_key");
    if (!seen.has(logo)) fail("theme.logo_asset_key has no asset");
  }
}

function validateLinks(value: unknown): void {
  const links = assertObject(value, "links");
  assertClosed(links, "links", ["support", "privacy", "terms", "custom"]);
  validateHTTPSURL(links.support, "links.support");
  validateHTTPSURL(links.privacy, "links.privacy");
  validateHTTPSURL(links.terms, "links.terms");
  if (links.custom === undefined) return;
  const custom = assertArray(links.custom, "links.custom", EXPERIENCE_LIMITS.customLinks);
  const seen = new Set<string>();
  for (const [index, linkValue] of custom.entries()) {
    const link = assertObject(linkValue, `links.custom[${index}]`);
    assertClosed(link, `links.custom[${index}]`, ["label", "url"]);
    assertText(link.label, `links.custom[${index}].label`, EXPERIENCE_LIMITS.nameBytes);
    const url = validateHTTPSURL(link.url, `links.custom[${index}].url`);
    if (seen.has(url)) fail(`links.custom[${index}].url duplicate`);
    seen.add(url);
  }
}

function validateTheme(value: unknown): Record<string, unknown> {
  const theme = assertObject(value, "theme");
  assertClosed(theme, "theme", [
    "primary_color",
    "accent_color",
    "background_color",
    "text_color",
    "logo_asset_key",
  ]);
  for (const field of [
    "primary_color",
    "accent_color",
    "background_color",
    "text_color",
  ] as const) {
    const color = assertString(theme[field], `theme.${field}`, 7);
    if (!COLOR_PATTERN.test(color)) fail(`theme.${field}`);
  }
  return theme;
}

/** validateExperienceDocument enforces the closed, bounded v1 contract. */
export function validateExperienceDocument(value: unknown): ExperienceDocument {
  const document = assertObject(value, "document");
  assertClosed(document, "document", [
    "schema_version",
    "experience_id",
    "version",
    "name",
    "copy",
    "mandatory_copy_version",
    "default_locale",
    "targeting",
    "assets",
    "links",
    "theme",
    "allowed_origins",
  ]);
  if (document.schema_version !== EXPERIENCE_SCHEMA_VERSION)
    fail("schema_version", "EXPERIENCE_UNSUPPORTED_VERSION");
  const identifier = assertString(document.experience_id, "experience_id", 64);
  if (!EXPERIENCE_ID_PATTERN.test(identifier)) fail("experience_id");
  if (
    typeof document.version !== "number" ||
    !Number.isInteger(document.version) ||
    document.version < 1
  ) {
    fail("version");
  }
  if (
    typeof document.name !== "string" ||
    document.name.length === 0 ||
    byteLength(document.name) > EXPERIENCE_LIMITS.nameBytes
  ) {
    fail("name");
  }
  if (
    typeof document.mandatory_copy_version !== "string" ||
    !MANDATORY_VERSION_PATTERN.test(document.mandatory_copy_version)
  ) {
    fail("mandatory_copy_version");
  }
  const defaultLocale = assertString(document.default_locale, "default_locale", 35);
  if (!LOCALE_PATTERN.test(defaultLocale)) fail("default_locale");
  validateCopy(document.copy);
  validateTargeting(document.targeting);
  const theme = validateTheme(document.theme);
  validateAssets(document.assets, theme);
  validateLinks(document.links);
  if (document.allowed_origins !== undefined) {
    const origins = assertArray(
      document.allowed_origins,
      "allowed_origins",
      EXPERIENCE_LIMITS.allowedOrigins,
    );
    origins.forEach((origin) => validateOrigin(origin, "allowed_origins"));
    assertUnique(origins as string[], "allowed_origins");
  }
  const copy = document.copy as { locales: readonly { locale: string }[] };
  if (!copy.locales.some((locale) => locale.locale === defaultLocale))
    fail("default_locale not in copy.locales");
  return value as ExperienceDocument;
}

/** isExperienceDocument reports whether the value satisfies the v1 contract. */
export function isExperienceDocument(value: unknown): value is ExperienceDocument {
  try {
    validateExperienceDocument(value);
    return true;
  } catch {
    return false;
  }
}

function encodeCanonicalString(value: string): string {
  let result = '"';
  for (const character of value) {
    switch (character) {
      case '"':
        result += '\\"';
        break;
      case "\\":
        result += "\\\\";
        break;
      case "\b":
        result += "\\b";
        break;
      case "\f":
        result += "\\f";
        break;
      case "\n":
        result += "\\n";
        break;
      case "\r":
        result += "\\r";
        break;
      case "\t":
        result += "\\t";
        break;
      default: {
        const codePoint = character.codePointAt(0) ?? 0;
        result += codePoint < 0x20 ? `\\u00${codePoint.toString(16).padStart(2, "0")}` : character;
      }
    }
  }
  return `${result}"`;
}

function encodeCanonical(value: unknown): string {
  if (value === null) return "null";
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "number") {
    if (!Number.isInteger(value) || value < 0) fail("canonical number");
    return String(value);
  }
  if (typeof value === "string") return encodeCanonicalString(value);
  if (Array.isArray(value)) return `[${value.map((item) => encodeCanonical(item)).join(",")}]`;
  const object = value as Record<string, unknown>;
  const keys = Object.keys(object).sort();
  return `{${keys.map((key) => `${encodeCanonicalString(key)}:${encodeCanonical(object[key])}`).join(",")}}`;
}

/**
 * canonicalExperienceBytes returns the canonical encoding shared by Go and
 * TypeScript. It validates first so invalid documents never receive a digest.
 */
export function canonicalExperienceBytes(value: unknown): Uint8Array {
  const document = validateExperienceDocument(value);
  const encoded = encodeCanonical(JSON.parse(JSON.stringify(document)) as unknown);
  if (byteLength(encoded) > EXPERIENCE_LIMITS.documentBytes)
    fail("document", "EXPERIENCE_TOO_LARGE");
  return new TextEncoder().encode(encoded);
}

function hexEncode(bytes: Uint8Array): string {
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

/** experienceDigest returns lowercase SHA-256 over canonical document bytes. */
export async function experienceDigest(value: unknown): Promise<string> {
  const bytes = canonicalExperienceBytes(value);
  const subtle = globalThis.crypto?.subtle;
  if (subtle === undefined) fail("crypto.subtle unavailable", "EXPERIENCE_CRYPTO_UNAVAILABLE");
  const digest = await subtle.digest("SHA-256", bytes as unknown as BufferSource);
  return hexEncode(new Uint8Array(digest));
}

function hexDecode(value: string): Uint8Array {
  const bytes = new Uint8Array(value.length / 2);
  for (let index = 0; index < bytes.length; index += 1) {
    bytes[index] = Number.parseInt(value.slice(index * 2, index * 2 + 2), 16);
  }
  return bytes;
}

/**
 * verifyExperienceManifest recomputes the canonical digest and verifies the
 * Ed25519 signature. Tampered documents, wrong keys, and unknown key ids throw.
 */
export async function verifyExperienceManifest(
  value: unknown,
  publicKeys: Readonly<Record<string, Uint8Array>>,
): Promise<ExperienceDocument> {
  const manifest = validateManifestShape(value);
  const digest = await experienceDigest(manifest.document);
  if (digest !== manifest.digest) fail("digest", "EXPERIENCE_SIGNATURE");
  const publicKey = publicKeys[manifest.key_id];
  if (publicKey === undefined || publicKey.length !== 32) {
    throw new ExperienceContractError(
      "experience v1: unknown signing key",
      "EXPERIENCE_UNKNOWN_KEY",
    );
  }
  const subtle = globalThis.crypto?.subtle;
  if (subtle === undefined) fail("crypto.subtle unavailable", "EXPERIENCE_CRYPTO_UNAVAILABLE");
  try {
    const key = await subtle.importKey(
      "raw",
      publicKey as unknown as BufferSource,
      { name: "Ed25519" },
      false,
      ["verify"],
    );
    const valid = await subtle.verify(
      { name: "Ed25519" },
      key,
      hexDecode(manifest.signature) as unknown as BufferSource,
      canonicalExperienceBytes(manifest.document) as unknown as BufferSource,
    );
    if (!valid) fail("signature", "EXPERIENCE_SIGNATURE");
  } catch (error) {
    if (error instanceof ExperienceContractError) throw error;
    throw new ExperienceContractError(
      "experience v1: signature verification failed",
      "EXPERIENCE_SIGNATURE",
    );
  }
  return manifest.document;
}

function validateManifestShape(value: unknown): ExperienceManifest {
  const manifest = assertObject(value, "manifest");
  assertClosed(manifest, "manifest", ["document", "digest", "key_id", "algorithm", "signature"]);
  validateExperienceDocument(manifest.document);
  if (typeof manifest.digest !== "string" || !DIGEST_PATTERN.test(manifest.digest)) fail("digest");
  if (manifest.algorithm !== EXPERIENCE_SIGNATURE_ALGORITHM) fail("algorithm");
  if (typeof manifest.key_id !== "string" || !KEY_ID_PATTERN.test(manifest.key_id)) fail("key_id");
  if (
    typeof manifest.signature !== "string" ||
    manifest.signature.length === 0 ||
    manifest.signature.length > 256 ||
    manifest.signature.length % 2 !== 0 ||
    !/^[0-9a-f]+$/.test(manifest.signature)
  ) {
    fail("signature");
  }
  return value as ExperienceManifest;
}

function validatePinnedShape(value: unknown): ExperiencePinned {
  const pinned = assertObject(value, "pinned");
  assertClosed(pinned, "pinned", [
    "experience_id",
    "version",
    "locale",
    "tenant_copy_version",
    "mandatory_copy_version",
    "source",
    "digest",
    "key_id",
    "pinned_at",
  ]);
  const identifier = assertString(pinned.experience_id, "pinned.experience_id", 64);
  if (!EXPERIENCE_ID_PATTERN.test(identifier)) fail("pinned.experience_id");
  if (
    typeof pinned.version !== "number" ||
    !Number.isInteger(pinned.version) ||
    pinned.version < 1
  ) {
    fail("pinned.version");
  }
  if (typeof pinned.locale !== "string" || !LOCALE_PATTERN.test(pinned.locale))
    fail("pinned.locale");
  if (
    typeof pinned.tenant_copy_version !== "string" ||
    !COPY_VERSION_PATTERN.test(pinned.tenant_copy_version)
  ) {
    fail("pinned.tenant_copy_version");
  }
  if (
    typeof pinned.mandatory_copy_version !== "string" ||
    !MANDATORY_VERSION_PATTERN.test(pinned.mandatory_copy_version)
  ) {
    fail("pinned.mandatory_copy_version");
  }
  if (pinned.source !== "pinned" && pinned.source !== "published" && pinned.source !== "default") {
    fail("pinned.source");
  }
  if (typeof pinned.digest !== "string" || !DIGEST_PATTERN.test(pinned.digest))
    fail("pinned.digest");
  if (typeof pinned.key_id !== "string" || !KEY_ID_PATTERN.test(pinned.key_id))
    fail("pinned.key_id");
  if (typeof pinned.pinned_at !== "string" || Number.isNaN(Date.parse(pinned.pinned_at))) {
    fail("pinned.pinned_at");
  }
  return value as ExperiencePinned;
}

function validateMandatoryCopyShape(value: unknown): ExperienceMandatoryCopy {
  const copy = assertObject(value, "mandatory_copy");
  assertClosed(copy, "mandatory_copy", ["version", "digest", "entries"]);
  if (typeof copy.version !== "string" || !MANDATORY_VERSION_PATTERN.test(copy.version)) {
    fail("mandatory_copy.version");
  }
  if (typeof copy.digest !== "string" || !DIGEST_PATTERN.test(copy.digest))
    fail("mandatory_copy.digest");
  const entries = assertArray(
    copy.entries,
    "mandatory_copy.entries",
    EXPERIENCE_LIMITS.copyEntriesPerLocale,
  );
  if (entries.length === 0) fail("mandatory_copy.entries");
  for (const [index, entry] of entries.entries()) {
    const record = assertObject(entry, `mandatory_copy.entries[${index}]`);
    assertClosed(record, `mandatory_copy.entries[${index}]`, ["key", "value"]);
    const key = assertString(
      record.key,
      `mandatory_copy.entries[${index}].key`,
      EXPERIENCE_LIMITS.copyKeyBytes,
    );
    if (!COPY_KEY_PATTERN.test(key)) fail(`mandatory_copy.entries[${index}].key`);
    assertText(
      record.value,
      `mandatory_copy.entries[${index}].value`,
      EXPERIENCE_LIMITS.copyValueBytes,
    );
  }
  return value as ExperienceMandatoryCopy;
}

/** validateExperienceManifest enforces the closed signed-envelope shape without verifying keys. */
export function validateExperienceManifest(value: unknown): ExperienceManifest {
  return validateManifestShape(value);
}

/** validateExperienceResolution enforces the closed bootstrap resolution shape. */
export function validateExperienceResolution(value: unknown): ExperienceResolution {
  const resolution = assertObject(value, "resolution");
  assertClosed(resolution, "resolution", ["manifest", "mandatory_copy", "pinned", "fallback"]);
  validateManifestShape(resolution.manifest);
  validateMandatoryCopyShape(resolution.mandatory_copy);
  validatePinnedShape(resolution.pinned);
  if (typeof resolution.fallback !== "boolean") fail("fallback");
  return value as ExperienceResolution;
}
