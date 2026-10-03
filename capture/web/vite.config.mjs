import { CaptureClient, IdenqaClient, createIdempotencyKey } from "@idenqa/sdk";
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

const documentOptionsByCountry = {
  NG: [
    {
      id: "nin",
      label: "National Identity Number (NIN)",
      artefacts: ["idenqa.artefact.document_front"],
    },
    {
      id: "driver_license",
      label: "Driver’s licence",
      artefacts: ["idenqa.artefact.document_front", "idenqa.artefact.document_back"],
    },
    { id: "passport", label: "Passport", artefacts: ["idenqa.artefact.document_front"] },
  ],
  GH: [
    {
      id: "ghana_card",
      label: "Ghana Card",
      artefacts: ["idenqa.artefact.document_front", "idenqa.artefact.document_back"],
    },
    {
      id: "driver_license",
      label: "Driver’s licence",
      artefacts: ["idenqa.artefact.document_front", "idenqa.artefact.document_back"],
    },
    { id: "passport", label: "Passport", artefacts: ["idenqa.artefact.document_front"] },
  ],
  GB: [
    {
      id: "driver_license",
      label: "Driving licence",
      artefacts: ["idenqa.artefact.document_front", "idenqa.artefact.document_back"],
    },
    { id: "passport", label: "Passport", artefacts: ["idenqa.artefact.document_front"] },
  ],
};

function documentProfile(country) {
  const options = documentOptionsByCountry[country];
  if (options === undefined) throw new Error("unsupported document country");
  return {
    ...demoProfile,
    requirements: [
      {
        ...demoProfile.requirements[0],
        key: "identity_document",
        evidence_type: "idenqa.evidence.document_image",
        artefacts: ["idenqa.artefact.document_front", "idenqa.artefact.document_back"],
        document_options: options,
      },
      demoProfile.requirements[0],
    ],
  };
}

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
      // Conformance must keep its loaded module graph stable while other work
      // rebuilds workspace packages; hot reload would silently restart sessions.
      ...(environment.IDENQA_DEMO_CONFORMANCE === "true" ? { hmr: false, watch: null } : {}),
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
              const launch = await readDemoLaunch(request);
              const outcome = requestedDemoOutcome(launch.outcome);
              const documentJourney = launch.journey === "document";
              const profileId = requestedCaptureProfile(launch.profile);
              const country = requestedDocumentCountry(launch.country);
              const noticeIdentity = requestedNoticeIdentity(launch.controller, launch.recipient);
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
                documentJourney,
                country,
                profileId,
                noticeIdentity,
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

async function provisionJourney({
  apiKey,
  coreUrl,
  outcome,
  policyId,
  region,
  documentJourney,
  country,
  profileId,
  noticeIdentity,
}) {
  const tenant = new IdenqaClient({ baseUrl: coreUrl, apiKey });
  const captureProfileId =
    profileId === undefined
      ? await provisionDemoProfile({ tenant, documentJourney, country })
      : await provisionCountryBoundProfile({ tenant, profileId, country });

  // Core's current authority services compare at whole-second precision. Keep
  // this synthetic fixture on that boundary so an immediate subject response
  // cannot fall fractionally before the declaration's valid-from instant.
  const now = new Date(Math.floor(Date.now() / 1_000) * 1_000);
  const verification = await runStage("create verification", () =>
    tenant.verifications.create(
      {
        captureProfileId,
        policyId,
        verificationTtlSeconds: outcome === "expired" ? 5 : 1800,
        captureTokenTtlSeconds: outcome === "expired" ? 5 : 1800,
        outcomeTokenPostExpiryTtlSeconds: outcome === "expired" ? 300 : 86400,
      },
      { idempotencyKey: createIdempotencyKey("demo_verification") },
    ),
  );
  const noticeCopy = demoNoticeCopy();
  const notice = await runStage("create notice", () =>
    tenant.notices.create(
      {
        key: `tenant.notice.capture_demo.${Date.now()}`,
        locale: "en",
        controller: noticeIdentity.controller,
        recipient: noticeIdentity.recipient,
        copy: {
          title: "Identity Verification Notice",
          summary: noticeCopy.summary,
          purpose: noticeCopy.purpose,
          consequences: "You may refuse. Capture will stop and no evidence will be collected.",
        },
        effectiveAt: now.toISOString(),
      },
      { idempotencyKey: createIdempotencyKey("demo_notice") },
    ),
  );
  await runStage("declare authority", () =>
    tenant.authorities.declare(
      verification.data.session.id,
      {
        noticeId: notice.data.id,
        category: "tenant.authority.customer_declared",
        purpose: "idenqa.purpose.identity_verification",
        jurisdiction:
          country === undefined
            ? "tenant.jurisdiction.local_demo"
            : `tenant.jurisdiction.country.${country.toLowerCase()}`,
        policyPack: "tenant.policy.local_demo_v1",
        consentRequired: true,
        recipientReference: "tenant.recipient.local_demo",
        recipientDisplayName: noticeIdentity.recipient,
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
  // Resolve and pin the portable experience server-side so the browser never
  // needs the tenant API key and the signed document reaches the component.
  let experience;
  try {
    const captureClient = new CaptureClient({
      baseUrl: coreUrl,
      captureToken: verification.data.captureToken,
    });
    experience = (
      await runStage("resolve experience", () =>
        captureClient.getExperience({ workflow: "capture.identity", locale: "en" }),
      )
    ).data;
  } catch {
    // A deployment without experience signing keys still runs the journey with
    // the package-owned safe default presentation.
    experience = undefined;
  }
  return {
    baseUrl: "/core/",
    verificationId: verification.data.session.id,
    captureToken: verification.data.captureToken,
    outcomeToken: verification.data.outcomeToken,
    sessionVersion: verification.data.session.version,
    region,
    outcome,
    ...selfieRequirementBinding(verification.data.session.requirements.requirements),
    ...(experience === undefined ? {} : { experience }),
  };
}

function selfieRequirementBinding(requirements) {
  const matches = requirements.filter(
    (requirement) =>
      requirement.evidence_type === "idenqa.evidence.selfie_image" &&
      requirement.artefacts.includes("idenqa.artefact.selfie_image"),
  );
  if (matches.length > 1) throw new Error("capture profile has ambiguous selfie requirements");
  return matches.length === 0 ? {} : { selfieRequirementKey: matches[0].key };
}

async function provisionDemoProfile({ tenant, documentJourney, country }) {
  const createdProfile = await runStage("create profile", () =>
    tenant.captureProfiles.create(
      {
        name: "Capture Web hosted demo",
        document: documentJourney ? documentProfile(country) : demoProfile,
      },
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
  return createdProfile.data.profileId;
}

async function provisionCountryBoundProfile({ tenant, profileId, country }) {
  const profile = await runStage("get profile", () => tenant.captureProfiles.get(profileId));
  if (profile.data.publishedRevision === undefined) {
    throw new Error("capture profile has no published revision");
  }
  const revision = await runStage("get profile revision", () =>
    tenant.captureProfiles.getRevision(profileId, profile.data.publishedRevision),
  );
  const countryOptions = documentOptionsByCountry[country];
  if (countryOptions === undefined) throw new Error("unsupported document country");
  let hasDocumentRequirement = false;
  const requirements = revision.data.document.requirements.map((requirement) => {
    if (requirement.evidence_type !== "idenqa.evidence.document_image") return requirement;
    hasDocumentRequirement = true;
    const permitted =
      requirement.document_options === undefined
        ? countryOptions
        : countryOptions.filter((option) =>
            requirement.document_options.some((candidate) => candidate.id === option.id),
          );
    if (permitted.length === 0) {
      throw new Error("capture profile permits no documents for the selected country");
    }
    return {
      ...requirement,
      artefacts: [...new Set(permitted.flatMap((option) => option.artefacts))],
      document_options: permitted,
    };
  });
  if (!hasDocumentRequirement) return profileId;
  const createdProfile = await runStage("create country profile", () =>
    tenant.captureProfiles.create(
      {
        name: `${profile.data.name} (${country} demo)`,
        document: { ...revision.data.document, requirements },
      },
      { idempotencyKey: createIdempotencyKey("demo_country_profile") },
    ),
  );
  const publishedProfile = await runStage("publish country profile", () =>
    tenant.captureProfiles.publish(createdProfile.data.profileId, {
      etag: required(createdProfile.etag, "profile ETag"),
      idempotencyKey: createIdempotencyKey("demo_country_profile_publish"),
    }),
  );
  if (publishedProfile.data.state !== "active") throw new Error("profile was not activated");
  return createdProfile.data.profileId;
}

function requestedCaptureProfile(value) {
  if (value === undefined) return undefined;
  if (!/^prf_[0-9A-HJKMNP-TV-Z]{26}$/.test(value)) {
    throw new Error("capture profile identifier is invalid");
  }
  return value;
}

function requestedDocumentCountry(value) {
  if (typeof value !== "string") throw new Error("document country is required");
  const country = value.trim().toUpperCase();
  if (documentOptionsByCountry[country] === undefined) {
    throw new Error("document country is unsupported");
  }
  return country;
}

async function readDemoLaunch(request) {
  if (!String(request.headers["content-type"] ?? "").startsWith("application/json")) {
    throw new Error("capture launch must be JSON");
  }
  const chunks = [];
  let size = 0;
  for await (const chunk of request) {
    size += chunk.length;
    if (size > 8_192) throw new Error("capture launch is too large");
    chunks.push(chunk);
  }
  const value = JSON.parse(Buffer.concat(chunks).toString("utf8"));
  if (value === null || Array.isArray(value) || typeof value !== "object") {
    throw new Error("capture launch is invalid");
  }
  return value;
}

function requestedNoticeIdentity(controller, recipient) {
  if (!validDisplayIdentity(controller) || !validDisplayIdentity(recipient)) {
    throw new Error("tenant notice identity is required");
  }
  return { controller: controller.trim(), recipient: recipient.trim() };
}

function validDisplayIdentity(value) {
  return typeof value === "string" && value.trim().length >= 2 && value.trim().length <= 200;
}

function demoNoticeCopy() {
  return {
    summary: "We need identity evidence to demonstrate this capture journey.",
    purpose: "This evidence is used only for this local identity-capture demonstration.",
  };
}

async function waitForVerificationState(tenant, verificationId, wanted) {
  // Expiry is durable maintenance work. Allow its 30-second initial backoff
  // after a legitimate serialisation retry instead of assuming first-attempt success.
  const deadline = Date.now() + 120_000;
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

function requestedDemoOutcome(value) {
  const outcome = value ?? "verified";
  if (typeof outcome !== "string") throw new Error("unsupported demo outcome");
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
