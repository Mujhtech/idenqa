import { createHash, timingSafeEqual } from "node:crypto";

import { IdenqaClient } from "@idenqa/sdk";

const captureProfile = {
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

const verifiedPolicy = {
  schema_major: 1,
  schema_minor: 0,
  verified_assurance: "synthetic.fixture",
  rules: [
    {
      name: "synthetic_verified",
      when: 'facts["synthetic.document"] == "satisfied"',
      result: {
        state: "satisfied",
        directive: "complete_verified",
        priority: 1,
        contributing_facts: ["synthetic.document"],
        reason_codes: [],
      },
    },
  ],
};

export function createCaptureBootstrapPlugin(configuration) {
  return {
    name: "idenqa-capture-bootstrap",
    configureServer(server) {
      server.middlewares.use("/capture/bootstrap", async (request, response) => {
        response.setHeader("Cache-Control", "no-store");
        response.setHeader("Content-Type", "application/json; charset=utf-8");
        if (request.method !== "POST") return send(response, 405, { error: "method_not_allowed" });
        if (!configured(configuration)) {
          return send(response, 503, { error: "capture_bootstrap_not_configured" });
        }
        if (request.headers["sec-fetch-site"] !== "same-origin") {
          return send(response, 403, { error: "cross_site_request_blocked" });
        }
        try {
          const launch = await readLaunch(request);
          if (!sameCredential(launch.launchCredential, configuration.launchCredential)) {
            return send(response, 401, { error: "launch_credential_invalid" });
          }
          return send(
            response,
            201,
            await provisionJourney(configuration, launch.launchCredential),
          );
        } catch (error) {
          server.config.logger.error(
            `[capture-bootstrap] provisioning failed: ${safeErrorMessage(error)}`,
          );
          return send(response, error instanceof LaunchError ? 400 : 502, {
            error: error instanceof LaunchError ? error.code : "core_bootstrap_failed",
          });
        }
      });
    },
  };
}

async function provisionJourney(configuration, launchCredential) {
  const tenant = new IdenqaClient({
    baseUrl: configuration.coreUrl,
    apiKey: configuration.apiKey,
  });
  const key = stageKey(launchCredential);
  const policy = await runStage("create policy", () =>
    tenant.policies.create(verifiedPolicy, { idempotencyKey: key("policy") }),
  );
  await runStage("activate policy", () =>
    tenant.policies.activate(
      policy.data.policy.id,
      {
        revision: policy.data.policy.latestRevision,
        expectedVersion: policy.data.policy.activationVersion,
        reason: "capture_web_fixture",
      },
      { idempotencyKey: key("policy_activation") },
    ),
  );
  const createdProfile = await runStage("create profile", () =>
    tenant.captureProfiles.create(
      { name: "Capture Web hosted fixture", document: captureProfile },
      { idempotencyKey: key("profile") },
    ),
  );
  const publishedProfile = await runStage("publish profile", () =>
    tenant.captureProfiles.publish(createdProfile.data.profileId, {
      etag: required(createdProfile.etag, "profile ETag"),
      idempotencyKey: key("profile_publish"),
    }),
  );
  if (publishedProfile.data.state !== "active") throw new Error("profile was not activated");

  const subjectResult = await runStage("create subject", () =>
    tenant.identity.createSubject(undefined, { idempotencyKey: key("subject") }),
  );
  const subject = activeSubject(subjectResult, configuration.region);
  const verification = await runStage("create verification", () =>
    tenant.verifications.create(
      {
        captureProfileId: createdProfile.data.profileId,
        policyId: policy.data.policy.id,
        verificationTtlSeconds: 1_800,
        captureTokenTtlSeconds: 1_800,
        outcomeTokenPostExpiryTtlSeconds: 86_400,
      },
      { idempotencyKey: key("verification") },
    ),
  );
  await runStage("link subject verification", () =>
    linkSubjectVerification({
      tenant,
      subjectId: subject.id,
      verificationId: verification.data.session.id,
      region: configuration.region,
      key,
    }),
  );

  const effectiveAt = new Date(
    Math.floor(Date.parse(verification.data.session.createdAt) / 1_000) * 1_000,
  ).toISOString();
  const notice = await runStage("create notice", () =>
    tenant.notices.create(
      {
        key: `tenant.notice.capture_fixture.${key("notice")}`,
        locale: "en",
        controller: configuration.controller,
        recipient: configuration.recipient,
        copy: {
          title: "Identity Verification Notice",
          summary: "We need identity evidence to demonstrate this capture journey.",
          purpose: "This evidence is used only for this local identity-capture fixture.",
          consequences: "You may refuse. Capture will stop and no evidence will be collected.",
        },
        effectiveAt,
      },
      { idempotencyKey: key("notice") },
    ),
  );
  await runStage("declare authority", () =>
    tenant.authorities.declare(
      verification.data.session.id,
      {
        noticeId: notice.data.id,
        category: "tenant.authority.customer_declared",
        purpose: "idenqa.purpose.identity_verification",
        jurisdiction: "tenant.jurisdiction.local_fixture",
        policyPack: "tenant.policy.local_fixture_v1",
        consentRequired: true,
        recipientReference: "tenant.recipient.local_fixture",
        recipientDisplayName: configuration.recipient,
        regions: [configuration.region],
        retentionReference: "tenant.retention.local_fixture",
        validFrom: notice.data.effectiveAt,
        expiresAt: verification.data.session.expiresAt,
      },
      { idempotencyKey: key("authority") },
    ),
  );
  return {
    baseUrl: "/core/",
    verificationId: verification.data.session.id,
    captureToken: verification.data.captureToken,
    outcomeToken: verification.data.outcomeToken,
    region: configuration.region,
  };
}

async function linkSubjectVerification({ tenant, subjectId, verificationId, region, key }) {
  for (let attempt = 0; attempt < 3; attempt++) {
    const subject = activeSubject(await tenant.identity.getSubject(subjectId), region);
    let after;
    do {
      const links = await tenant.identity.listVerifications(subjectId, {
        limit: 100,
        ...(after === undefined ? {} : { after }),
      });
      if (links.data.verification_ids?.includes(verificationId)) return;
      after = links.data.next_cursor;
    } while (after !== undefined && after !== "");
    try {
      await tenant.identity.linkVerification(subjectId, verificationId, subject.version, {
        idempotencyKey: key(`subject_link_v${subject.version}`),
      });
      return;
    } catch (error) {
      if (error?.code !== "CONFLICT" || attempt === 2) throw error;
    }
  }
}

function activeSubject(result, region) {
  const subject = required(result.data.subject, "persistent subject");
  if (subject.state !== "active" || subject.region !== region) {
    throw new Error("the fixture subject is unavailable in the capture region");
  }
  return subject;
}

async function readLaunch(request) {
  if (!String(request.headers["content-type"] ?? "").startsWith("application/json")) {
    throw new LaunchError("launch_content_type_invalid", "The capture launch must be JSON.");
  }
  const chunks = [];
  let size = 0;
  for await (const chunk of request) {
    size += chunk.length;
    if (size > 4_096) throw new LaunchError("launch_too_large", "The capture launch is too large.");
    chunks.push(chunk);
  }
  let value;
  try {
    value = JSON.parse(Buffer.concat(chunks).toString("utf8"));
  } catch {
    throw new LaunchError("launch_invalid", "The capture launch is invalid.");
  }
  if (value === null || Array.isArray(value) || typeof value !== "object") {
    throw new LaunchError("launch_invalid", "The capture launch is invalid.");
  }
  const names = Object.keys(value);
  if (names.length !== 1 || names[0] !== "launchCredential") {
    throw new LaunchError("launch_invalid", "Only an opaque launch credential is accepted.");
  }
  if (
    typeof value.launchCredential !== "string" ||
    value.launchCredential.length < 16 ||
    value.launchCredential.length > 2_048
  ) {
    throw new LaunchError("launch_credential_invalid", "The launch credential is invalid.");
  }
  return value;
}

function configured(configuration) {
  return (
    nonempty(configuration.coreUrl) &&
    nonempty(configuration.apiKey) &&
    nonempty(configuration.region) &&
    typeof configuration.launchCredential === "string" &&
    configuration.launchCredential.length >= 16 &&
    configuration.launchCredential.length <= 2_048 &&
    displayIdentity(configuration.controller) &&
    displayIdentity(configuration.recipient)
  );
}

function nonempty(value) {
  return typeof value === "string" && value.trim().length > 0;
}

function displayIdentity(value) {
  return nonempty(value) && value.trim().length >= 2 && value.trim().length <= 200;
}

function sameCredential(candidate, expected) {
  const candidateHash = createHash("sha256").update(candidate).digest();
  const expectedHash = createHash("sha256").update(expected).digest();
  return timingSafeEqual(candidateHash, expectedHash);
}

function stageKey(launchCredential) {
  const digest = createHash("sha256").update(launchCredential).digest("hex").slice(0, 32);
  return (stage) => `capture_fixture_${digest}_${stage}`;
}

function send(response, status, body) {
  response.statusCode = status;
  if (status === 405) response.setHeader("Allow", "POST");
  response.end(JSON.stringify(body));
}

function required(value, label) {
  if (value === undefined) throw new Error(`${label} is missing`);
  return value;
}

class LaunchError extends Error {
  constructor(code, message) {
    super(message);
    this.name = "LaunchError";
    this.code = code;
  }
}

async function runStage(name, operation) {
  try {
    return await operation();
  } catch (error) {
    throw new Error(`${name}: ${safeErrorMessage(error)}`, { cause: error });
  }
}

function safeErrorMessage(error) {
  if (!(error instanceof Error)) return "unknown error";
  const code = typeof error.code === "string" ? ` code=${error.code}` : "";
  const status = typeof error.status === "number" ? ` status=${error.status}` : "";
  return `${error.name}: ${error.message}${code}${status}`.replaceAll(
    /(Bearer|credential|token)\s+[^\s]+/gi,
    "$1 [REDACTED]",
  );
}
