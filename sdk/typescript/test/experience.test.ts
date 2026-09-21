import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import {
  ExperienceContractError,
  canonicalExperienceBytes,
  experienceDigest,
  validateExperienceDocument,
  verifyExperienceManifest,
  type ExperienceDocument,
  type ExperienceManifest,
} from "../src/index.js";

interface Vector {
  readonly public_key_hex: string;
  readonly canonical_digest: string;
  readonly manifest: ExperienceManifest;
}

function loadVector(): Vector {
  const path = fileURLToPath(
    new URL("../../../contracts/experience/v1/testdata/manifest.json", import.meta.url),
  );
  return JSON.parse(readFileSync(path, "utf8")) as Vector;
}

function clone(document: ExperienceDocument): Record<string, unknown> {
  return JSON.parse(JSON.stringify(document)) as Record<string, unknown>;
}

describe("portable experience contract", () => {
  it("matches the shared Go canonical digest and verifies the manifest", async () => {
    const vector = loadVector();
    const document = validateExperienceDocument(vector.manifest.document);
    const digest = await experienceDigest(document);
    expect(digest).toBe(vector.canonical_digest);
    expect(vector.manifest.digest).toBe(vector.canonical_digest);

    const publicKey = new Uint8Array(Buffer.from(vector.public_key_hex, "hex"));
    const verified = await verifyExperienceManifest(vector.manifest, {
      [vector.manifest.key_id]: publicKey,
    });
    expect(verified.experience_id).toBe(vector.manifest.document.experience_id);
  });

  it("produces identical canonical bytes to the fixed digest input", async () => {
    const vector = loadVector();
    const canonical = new TextDecoder().decode(canonicalExperienceBytes(vector.manifest.document));
    expect(canonical.startsWith('{"allowed_origins":[')).toBe(true);
    expect(canonical).not.toContain("\\u003c");
    expect(canonical).toContain('"version":1');
  });

  it("rejects tampered documents, wrong keys, and unknown key ids", async () => {
    const vector = loadVector();
    const publicKey = new Uint8Array(Buffer.from(vector.public_key_hex, "hex"));
    const tampered = {
      ...vector.manifest,
      document: { ...vector.manifest.document, name: "Hijacked capture" },
    };
    await expect(
      verifyExperienceManifest(tampered, { [vector.manifest.key_id]: publicKey }),
    ).rejects.toThrow(ExperienceContractError);
    const wrongKey = new Uint8Array(32);
    await expect(
      verifyExperienceManifest(vector.manifest, { [vector.manifest.key_id]: wrongKey }),
    ).rejects.toThrow(ExperienceContractError);
    await expect(verifyExperienceManifest(vector.manifest, {})).rejects.toThrow(
      ExperienceContractError,
    );
  });

  it("enforces the closed bounded contract", () => {
    const vector = loadVector();
    const document = vector.manifest.document;

    const reserved = clone(document);
    const copy = reserved.copy as { locales: { entries: { key: string; value: string }[] }[] };
    copy.locales[0]?.entries.push({ key: "consent.explicit", value: "override" });
    expect(() => validateExperienceDocument(reserved)).toThrow(ExperienceContractError);

    const executable = clone(document);
    const assets = executable.assets as { mime: string }[];
    assets[0]!.mime = "image/svg+xml";
    expect(() => validateExperienceDocument(executable)).toThrow(ExperienceContractError);

    const insecure = clone(document);
    (insecure.links as { support: string }).support = "http://support.acme.example";
    expect(() => validateExperienceDocument(insecure)).toThrow(ExperienceContractError);

    const unknown = clone(document);
    unknown.unexpected = true;
    expect(() => validateExperienceDocument(unknown)).toThrow(ExperienceContractError);

    const oversize = clone(document);
    (oversize.assets as { size: number }[])[0]!.size = 2 * 1024 * 1024 + 1;
    expect(() => validateExperienceDocument(oversize)).toThrow(ExperienceContractError);
  });
});
