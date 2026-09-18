import { IdenqaClient, createIdempotencyKey } from "@idenqa/sdk";
import { defineConfig, loadEnv } from "vite";

import { createReviewDemoPlugin } from "./demo/review-server.mjs";

const demoProfile = {
  schema_version: 1,
  registry: {
    schema_version: 1,
    revision: 1,
    digest: "sha256:71ef9df77044f9bf5eeb7ae448da3d98811ff4cb3f2541f459e60b4628a5364f",
  },
  requirements: [
    {
      key: "selfie",
      purpose: "idenqa.purpose.identity_verification",
      evidence_type: "idenqa.evidence.selfie_image",
      artefacts: ["idenqa.artefact.selfie_image"],
      acquisition: {
        strategy: "any_of",
        methods: ["idenqa.method.live_camera", "idenqa.method.file_upload"],
      },
      required_assurances: [],
      constraints: [
        {
          name: "idenqa.constraint.allowed_media_types",
          value: ["image/jpeg", "image/png"],
        },
      ],
      fallbacks: [],
    },
  ],
};

const demoOutcomes = new Set([
  "verified",
  "not_verified",
  "inconclusive",
  "action_required",
  "cancelled",
  "expired",
  "failed",
]);

const demoPolicyResults = {
  verified: { state: "satisfied", directive: "complete_verified" },
  not_verified: { state: "not_satisfied", directive: "complete_not_verified" },
  inconclusive: { state: "inconclusive", directive: "complete_inconclusive" },
  action_required: { state: "unavailable", directive: "request_input" },
  cancelled: { state: "satisfied", directive: "complete_verified" },
  expired: { state: "satisfied", directive: "complete_verified" },
  failed: { state: "prohibited", directive: "fail_workflow" },
};

export default defineConfig(({ mode }) => {
  const environment = loadEnv(mode, process.cwd(), "IDENQA_DEMO_");
  const coreUrl = normalizedCoreUrl(environment.IDENQA_DEMO_CORE_URL);
  const apiKey = environment.IDENQA_DEMO_TENANT_API_KEY;
  const apiKeyID = environment.IDENQA_DEMO_TENANT_API_KEY_ID;
  const region = environment.IDENQA_DEMO_REGION;
  const tenantID = environment.IDENQA_DEMO_TENANT_ID;

  return {
    server: {
      headers: {
        "Referrer-Policy": "no-referrer",
        "X-Content-Type-Options": "nosniff",
      },
      ...(coreUrl === undefined
        ? {}
        : {
            proxy: {
              "/core": {
                target: coreUrl,
                changeOrigin: true,
                ws: true,
                rewrite: (path) => path.replace(/^\/core/, ""),
              },
            },
          }),
    },
    plugins: [
      {
        name: "idenqa-real-core-demo-bootstrap",
        configureServer(server) {
          const policyIdPromises = new Map();
          server.middlewares.use("/__idenqa_demo/bootstrap", async (request, response) => {
            response.setHeader("Cache-Control", "no-store");
            response.setHeader("Content-Type", "application/json; charset=utf-8");
            if (request.method !== "POST") {
              response.statusCode = 405;
              response.setHeader("Allow", "POST");
              response.end(JSON.stringify({ error: "method_not_allowed" }));
              return;
            }
            if (coreUrl === undefined || apiKey === undefined || region === undefined) {
              response.statusCode = 503;
              response.end(JSON.stringify({ error: "demo_not_configured" }));
              return;
            }
            if (request.headers["sec-fetch-site"] !== "same-origin") {
              response.statusCode = 403;
              response.end(JSON.stringify({ error: "cross_site_request_blocked" }));
              return;
            }
            try {
              const outcome = requestedDemoOutcome(request.url);
              let policyIdPromise = policyIdPromises.get(outcome);
              policyIdPromise ??= provisionDemoPolicy({ apiKey, coreUrl, outcome }).catch(
                (error) => {
                  policyIdPromises.delete(outcome);
                  throw error;
                },
              );
              policyIdPromises.set(outcome, policyIdPromise);
              const policyId = await policyIdPromise;
              const journey = await provisionJourney({
                apiKey,
                coreUrl,
                outcome,
                policyId,
                region,
              });
              response.statusCode = 201;
              response.end(JSON.stringify(journey));
            } catch (error) {
              server.config.logger.error(
                `[capture-web-demo] bootstrap failed: ${safeErrorMessage(error)}`,
              );
              response.statusCode = 502;
              response.end(JSON.stringify({ error: "core_bootstrap_failed" }));
            }
          });
        },
      },
      createReviewDemoPlugin({ apiKey, apiKeyID, coreUrl, region, tenantID }),
    ],
  };
});

async function provisionDemoPolicy({ apiKey, coreUrl, outcome }) {
  const tenant = new IdenqaClient({ baseUrl: coreUrl, apiKey });
  const policy = await runStage("create policy", () =>
    tenant.policies.create(demoPolicy(outcome), {
      idempotencyKey: createIdempotencyKey("demo_policy"),
    }),
  );
  await runStage("activate policy", () =>
    tenant.policies.activate(
      policy.data.policy.id,
      {
        revision: policy.data.policy.latestRevision,
        expectedVersion: policy.data.policy.activationVersion,
        reason: "capture_web_conformance",
      },
      { idempotencyKey: createIdempotencyKey("demo_policy_activation") },
    ),
  );
  return policy.data.policy.id;
}

async function provisionJourney({ apiKey, coreUrl, outcome, policyId, region }) {
  const tenant = new IdenqaClient({ baseUrl: coreUrl, apiKey });
  const createdProfile = await runStage("create profile", () =>
    tenant.captureProfiles.create(
      { name: "Capture Web hosted demo", document: demoProfile },
      { idempotencyKey: createIdempotencyKey("demo_profile") },
    ),
  );
  const publishedProfile = await runStage("publish profile", () =>
    tenant.captureProfiles.publish(createdProfile.data.profileId, {
      etag: required(createdProfile.etag, "profile ETag"),
      idempotencyKey: createIdempotencyKey("demo_publish"),
    }),
  );
  if (publishedProfile.data.state !== "active") throw new Error("profile was not activated");

  // Core's current authority services compare at whole-second precision. Keep
  // this synthetic fixture on that boundary so an immediate subject response
  // cannot fall fractionally before the declaration's valid-from instant.
  const now = new Date(Math.floor(Date.now() / 1_000) * 1_000);
  const notice = await runStage("create notice", () =>
    tenant.notices.create(
      {
        key: `tenant.notice.capture_demo.${Date.now()}`,
        locale: "en",
        controller: "Idenqa local demo tenant",
        recipient: "Idenqa local demo tenant",
        copy: {
          title: "Identity Verification Notice",
          summary: "We need a synthetic selfie image to demonstrate the capture journey.",
          purpose:
            "The synthetic image is used only for this local identity-capture demonstration.",
          consequences: "You may refuse. Capture will stop and no evidence will be collected.",
        },
        effectiveAt: now.toISOString(),
      },
      { idempotencyKey: createIdempotencyKey("demo_notice") },
    ),
  );
  const verification = await runStage("create verification", () =>
    tenant.verifications.create(
      {
        captureProfileId: createdProfile.data.profileId,
        policyId,
        verificationTtlSeconds: outcome === "expired" ? 5 : 1800,
        captureTokenTtlSeconds: outcome === "expired" ? 5 : 1800,
        outcomeTokenPostExpiryTtlSeconds: outcome === "expired" ? 60 : 86400,
      },
      { idempotencyKey: createIdempotencyKey("demo_verification") },
    ),
  );
  await runStage("declare authority", () =>
    tenant.authorities.declare(
      verification.data.session.id,
      {
        noticeId: notice.data.id,
        category: "tenant.authority.customer_declared",
        purpose: "idenqa.purpose.identity_verification",
        jurisdiction: "tenant.jurisdiction.local_demo",
        policyPack: "tenant.policy.local_demo_v1",
        consentRequired: true,
        recipientReference: "tenant.recipient.local_demo",
        recipientDisplayName: "Idenqa local demo tenant",
        regions: [region],
        retentionReference: "tenant.retention.local_demo",
        validFrom: notice.data.effectiveAt,
        expiresAt: verification.data.session.expiresAt,
      },
      { idempotencyKey: createIdempotencyKey("demo_authority") },
    ),
  );
  if (outcome === "expired") {
    await waitForVerificationState(tenant, verification.data.session.id, "expired");
  }
  return {
    baseUrl: "/core/",
    verificationId: verification.data.session.id,
    captureToken: verification.data.captureToken,
    outcomeToken: verification.data.outcomeToken,
    sessionVersion: verification.data.session.version,
    region,
    outcome,
  };
}

async function waitForVerificationState(tenant, verificationId, wanted) {
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    const current = await tenant.verifications.get(verificationId);
    if (current.data.state === wanted) return;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`verification did not reach ${wanted}`);
}

function demoPolicy(outcome) {
  const result = demoPolicyResults[outcome];
  if (result === undefined) throw new Error("unsupported demo outcome");
  return {
    schema_major: 1,
    schema_minor: 0,
    verified_assurance: "synthetic.fixture",
    rules: [
      {
        name: `synthetic_${outcome}`,
        when: 'facts["synthetic.document"] == "satisfied"',
        result: {
          ...result,
          priority: 1,
          contributing_facts: ["synthetic.document"],
          reason_codes: [],
        },
      },
    ],
  };
}

function requestedDemoOutcome(requestURL) {
  const value = new URL(requestURL ?? "", "http://capture-web.invalid").searchParams.get("outcome");
  const outcome = value ?? "verified";
  if (!demoOutcomes.has(outcome)) throw new Error("unsupported demo outcome");
  return outcome;
}

function normalizedCoreUrl(value) {
  if (value === undefined || value.trim() === "") return undefined;
  const url = new URL(value);
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new Error("IDENQA_DEMO_CORE_URL must use HTTP or HTTPS");
  }
  url.pathname = `${url.pathname.replace(/\/+$/, "")}/`;
  url.search = "";
  url.hash = "";
  return url.href;
}

function required(value, label) {
  if (value === undefined) throw new Error(`${label} is missing`);
  return value;
}

function safeErrorMessage(error) {
  if (!(error instanceof Error)) return "unknown error";
  const context = error;
  const cause = error.cause instanceof Error ? ` cause=${error.cause.message}` : "";
  const code = typeof context.code === "string" ? ` code=${context.code}` : "";
  const status = typeof context.status === "number" ? ` status=${context.status}` : "";
  return `${error.name}: ${error.message}${code}${status}${cause}`.replaceAll(
    /(Bearer|credential|token)\s+[^\s]+/gi,
    "$1 [REDACTED]",
  );
}

async function runStage(name, operation) {
  try {
    return await operation();
  } catch (error) {
    throw new Error(`${name}: ${safeErrorMessage(error)}`, { cause: error });
  }
}
