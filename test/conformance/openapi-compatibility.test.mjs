import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { test } from "node:test";

const approved = [
  "created",
  "collecting",
  "awaiting_input",
  "processing",
  "awaiting_external",
  "manual_review",
  "completed",
  "cancelled",
  "expired",
  "failed",
];
const allowList = resolve("contracts/api/openapi/v1/lifecycle-alpha-warnings.txt");

function spec(values, { path = "/v1/capture/session", property = "state", status = "200" } = {}) {
  return {
    openapi: "3.0.3",
    info: { title: "Compatibility regression", version: "0.1.0" },
    paths: {
      [path]: {
        get: {
          responses: {
            [status]: {
              description: "Session",
              content: {
                "application/json": {
                  schema: {
                    type: "object",
                    properties: { [property]: { type: "string", enum: values } },
                  },
                },
              },
            },
          },
        },
      },
    },
  };
}

function compare(t, base, revision, useException = true) {
  const directory = mkdtempSync(join(tmpdir(), "idenqa-openapi-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const basePath = join(directory, "base.json");
  const revisionPath = join(directory, "revision.json");
  writeFileSync(basePath, JSON.stringify(base));
  writeFileSync(revisionPath, JSON.stringify(revision));
  const result = spawnSync(
    process.env.GO ?? "go",
    [
      "tool",
      "oasdiff",
      "breaking",
      "--lang",
      "en",
      "--fail-on",
      "WARN",
      ...(useException ? ["--warn-ignore", allowList] : []),
      "--",
      basePath,
      revisionPath,
    ],
    { encoding: "utf8", timeout: 60000 },
  );
  assert.ifError(result.error);
  assert.notEqual(result.status, null, result.stderr);
  return result;
}

test("the alpha state expansion requires its explicit exception", (t) => {
  const base = spec(["collecting"]);
  assert.equal(compare(t, base, spec(approved), false).status, 1);
  const accepted = compare(t, base, spec(approved));
  assert.equal(accepted.status, 0, accepted.stdout + accepted.stderr);
});

test("unapproved workflow values still fail", (t) => {
  const result = compare(t, spec(["collecting"]), spec([...approved, "verified"]));
  assert.equal(result.status, 1, result.stdout + result.stderr);
  assert.match(result.stdout, /verified/);
});

for (const [name, options] of [
  ["another endpoint", { path: "/v1/other/session" }],
  ["another property", { property: "outcome" }],
  ["another response status", { status: "201" }],
]) {
  test(`approved values on ${name} still fail`, (t) => {
    const result = compare(t, spec(["collecting"], options), spec(approved, options));
    assert.equal(result.status, 1, result.stdout + result.stderr);
  });
}

test("removing an existing operation still fails", (t) => {
  const revision = spec(approved);
  revision.paths = {};
  const result = compare(t, spec(approved), revision);
  assert.equal(result.status, 1, result.stdout + result.stderr);
});
