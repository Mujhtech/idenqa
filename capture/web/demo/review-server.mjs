import { randomUUID } from "node:crypto";

import { IdenqaClient, OutcomeClient, createIdempotencyKey } from "@idenqa/sdk";

const reviewProfile = {
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
        methods: ["idenqa.method.file_upload"],
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

export function createReviewDemoPlugin({ apiKey, apiKeyID, coreUrl, region, tenantID }) {
  const journeys = new Map();
  let operatorPromise;

  return {
    name: "idenqa-composed-review-demo",
    configureServer(server) {
      server.middlewares.use(async (request, response, next) => {
        const url = new URL(request.url ?? "/", "http://capture-web.invalid");
        if (!url.pathname.startsWith("/__idenqa_demo/review/")) {
          next();
          return;
        }
        response.setHeader("Cache-Control", "no-store, private");
        response.setHeader("Referrer-Policy", "no-referrer");
        response.setHeader("X-Content-Type-Options", "nosniff");
        if (request.method !== "POST") {
          writeJSON(response, 405, { error: "method_not_allowed" });
          return;
        }
        if (request.headers["sec-fetch-site"] !== "same-origin") {
          writeJSON(response, 403, { error: "cross_site_request_blocked" });
          return;
        }
        if (
          apiKey === undefined ||
          apiKeyID === undefined ||
          coreUrl === undefined ||
          region === undefined ||
          tenantID === undefined
        ) {
          writeJSON(response, 503, { error: "demo_not_configured" });
          return;
        }
        try {
          const segments = url.pathname.split("/").filter(Boolean);
          const operation = segments.at(-1);
          if (operation === "bootstrap" && segments.length === 3) {
            operatorPromise ??= provisionOperator({
              apiKey,
              apiKeyID,
              coreUrl,
              region,
              tenantID,
            }).catch((error) => {
              operatorPromise = undefined;
              throw error;
            });
            await operatorPromise;
            const journey = await provisionReviewJourney({
              apiKey,
              coreUrl,
              region,
              tenantID,
            });
            journeys.set(journey.id, journey);
            writeJSON(response, 201, {
              journeyId: journey.id,
              originalSubjectUrl: `/review-subject.html?journey=${encodeURIComponent(journey.id)}&stage=original`,
            });
            return;
          }
          if (segments.length !== 4) {
            writeJSON(response, 404, { error: "not_found" });
            return;
          }
          const journey = journeys.get(segments[2]);
          if (journey === undefined) {
            writeJSON(response, 404, { error: "journey_not_found" });
            return;
          }
          switch (operation) {
            case "handoff":
              writeJSON(response, 200, handoff(journey, url.searchParams.get("stage")));
              return;
            case "case": {
              const reviewCase = await waitForCase(journey);
              writeJSON(response, 200, caseSummary(reviewCase));
              return;
            }
            case "evidence": {
              const bytes = await claimAndReadEvidence(journey);
              response.statusCode = 200;
              response.setHeader("Content-Type", "image/png");
              response.setHeader("Content-Security-Policy", "default-src 'none'; sandbox");
              response.end(Buffer.from(bytes));
              return;
            }
            case "recapture": {
              const credential = await requestRecapture(journey);
              writeJSON(response, 201, {
                verificationId: credential.verification_id,
                subjectUrl: `/review-subject.html?journey=${encodeURIComponent(journey.id)}&stage=recapture`,
              });
              return;
            }
            case "result": {
              const result = await childResult(journey);
              writeJSON(response, result.complete ? 200 : 202, result);
              return;
            }
            case "complete": {
              const outcome = await completeReview(journey);
              writeJSON(response, 200, outcome);
              return;
            }
            default:
              writeJSON(response, 404, { error: "not_found" });
          }
        } catch (error) {
          server.config.logger.error(`[review-demo] request failed: ${safeErrorMessage(error)}`);
          writeJSON(response, 502, { error: "review_demo_failed" });
        }
      });
    },
  };
}

async function provisionOperator({ apiKey, apiKeyID, coreUrl, region, tenantID }) {
  const tenant = new IdenqaClient({ baseUrl: coreUrl, apiKey });
  const now = new Date();
  const configuration = {
    assignment: {
      tenant_id: tenantID,
      api_key_id: apiKeyID,
      operator_id: "reviewer.local_demo",
      permissions: ["reviews:claim", "reviews:find", "reviews:resolve"],
      certifications: ["identity.review"],
      regions: [region],
      not_before: new Date(now.getTime() - 60_000).toISOString(),
      expires_at: new Date(now.getTime() + 3_600_000).toISOString(),
    },
    revoked: false,
  };
  await runStage("configure review operator", () =>
    tenant.reviews.putReviewOperator(
      apiKeyID,
      {
        expected_version: 0,
        configuration,
      },
      { idempotencyKey: createIdempotencyKey("review_demo_operator") },
    ),
  );
}

async function provisionReviewJourney({ apiKey, coreUrl, region, tenantID }) {
  const tenant = new IdenqaClient({ baseUrl: coreUrl, apiKey });
  const policy = await runStage("create review policy", () =>
    tenant.policies.create(reviewPolicy(), {
      idempotencyKey: createIdempotencyKey("review_demo_policy"),
    }),
  );
  const policyID = policy.data.policy.id;
  const policyRevision = policy.data.policy.latestRevision;
  const revision = await runStage("read review policy revision", () =>
    tenant.policies.getRevision(policyID, policyRevision),
  );
  await runStage("configure review policy", () =>
    tenant.reviews.putReviewPolicy(
      policyID,
      policyRevision,
      {
        expected_version: 0,
        configuration: {
          tenant_id: tenantID,
          policy_id: policyID,
          policy_digest: revision.data.digest,
          required_certificate: "identity.review",
          oversight: "single",
          language: "en",
          reason: "selfie_quality",
          assurance: "standard",
          risk: "normal",
          escalation_certificate: "identity.supervisor",
          revision: policyRevision,
          permitted_findings: [{ resolution: "request_input", reason_code: "image_unclear" }],
          display: [
            {
              requirement: "selfie",
              purpose: "idenqa.purpose.identity_verification",
              redactions: [],
            },
          ],
          priority: 5,
          sla_seconds: 300,
          sample_percent: 0,
          appeal_window_seconds: 3600,
        },
      },
      { idempotencyKey: createIdempotencyKey("review_demo_policy_settings") },
    ),
  );
  await runStage("activate review policy", () =>
    tenant.policies.activate(
      policyID,
      {
        revision: policyRevision,
        expectedVersion: policy.data.policy.activationVersion,
        reason: "review_demo",
      },
      { idempotencyKey: createIdempotencyKey("review_demo_policy_activation") },
    ),
  );
  const profile = await runStage("create review capture profile", () =>
    tenant.captureProfiles.create(
      { name: "Review recapture demo", document: reviewProfile },
      { idempotencyKey: createIdempotencyKey("review_demo_profile") },
    ),
  );
  const published = await runStage("publish review capture profile", () =>
    tenant.captureProfiles.publish(profile.data.profileId, {
      etag: required(profile.etag, "review profile ETag"),
      idempotencyKey: createIdempotencyKey("review_demo_profile_publish"),
    }),
  );
  if (published.data.state !== "active") throw new Error("review profile was not activated");
  const now = new Date(Math.floor(Date.now() / 1_000) * 1_000);
  const notice = await runStage("create review notice", () =>
    tenant.notices.create(
      {
        key: `tenant.notice.review_demo.${Date.now()}`,
        locale: "en",
        controller: "Idenqa local demo tenant",
        recipient: "Idenqa local demo tenant",
        copy: {
          title: "Identity Verification Notice",
          summary: "We need a synthetic selfie image to demonstrate review and recapture.",
          purpose: "The image is used only for this local composed review demonstration.",
          consequences: "You may refuse. Capture will stop and no evidence will be collected.",
        },
        effectiveAt: now.toISOString(),
      },
      { idempotencyKey: createIdempotencyKey("review_demo_notice") },
    ),
  );
  const verification = await runStage("create reviewed verification", () =>
    tenant.verifications.create(
      {
        captureProfileId: profile.data.profileId,
        policyId: policyID,
        verificationTtlSeconds: 1800,
        captureTokenTtlSeconds: 1800,
        outcomeTokenPostExpiryTtlSeconds: 86400,
      },
      { idempotencyKey: createIdempotencyKey("review_demo_verification") },
    ),
  );
  await declareAuthority({
    tenant,
    verificationID: verification.data.session.id,
    verificationExpiresAt: verification.data.session.expiresAt,
    notice: notice.data,
    region,
    idempotencyPrefix: "review_demo_parent_authority",
  });
  return {
    id: randomUUID(),
    tenant,
    coreUrl,
    region,
    notice: notice.data,
    parent: {
      verificationId: verification.data.session.id,
      captureToken: verification.data.captureToken,
      outcomeToken: verification.data.outcomeToken,
      outcomeExpiresAt: verification.data.outcomeTokenExpiresAt,
      expiresAt: verification.data.session.expiresAt,
    },
  };
}

async function declareAuthority({
  idempotencyPrefix,
  notice,
  region,
  tenant,
  verificationExpiresAt,
  verificationID,
}) {
  await runStage("declare review journey authority", () =>
    tenant.authorities.declare(
      verificationID,
      {
        noticeId: notice.id,
        category: "tenant.authority.customer_declared",
        purpose: "idenqa.purpose.identity_verification",
        jurisdiction: "tenant.jurisdiction.local_demo",
        policyPack: "tenant.policy.local_demo_v1",
        consentRequired: true,
        recipientReference: "tenant.recipient.local_demo",
        recipientDisplayName: "Idenqa local demo tenant",
        regions: [region],
        retentionReference: "tenant.retention.local_demo",
        validFrom: notice.effectiveAt,
        expiresAt: verificationExpiresAt,
      },
      { idempotencyKey: createIdempotencyKey(idempotencyPrefix) },
    ),
  );
}

function reviewPolicy() {
  return {
    schema_major: 1,
    schema_minor: 0,
    verified_assurance: "synthetic.fixture",
    rules: [
      {
        name: "reviewed_recapture_accepted",
        when: '"review.recapture" in facts && facts["review.recapture"] == "satisfied" && facts["synthetic.document"] == "satisfied"',
        result: {
          state: "satisfied",
          directive: "complete_verified",
          priority: 1,
          contributing_facts: ["review.recapture", "synthetic.document"],
          reason_codes: ["review_recapture_accepted"],
        },
      },
      {
        name: "linked_recapture_completed",
        when: '"review.recapture.requested" in facts && facts["review.recapture.requested"] == "satisfied" && facts["synthetic.document"] == "satisfied"',
        result: {
          state: "satisfied",
          directive: "complete_verified",
          priority: 1,
          contributing_facts: ["review.recapture.requested", "synthetic.document"],
          reason_codes: ["linked_recapture_completed"],
        },
      },
      {
        name: "review_requests_input",
        when: '!("review.recapture" in facts) && "review.resolution" in facts && facts["review.resolution"] == "inconclusive" && facts["synthetic.document"] == "satisfied"',
        result: {
          state: "inconclusive",
          directive: "request_input",
          priority: 2,
          contributing_facts: ["review.resolution", "synthetic.document"],
          reason_codes: ["review_requested_input"],
        },
      },
      {
        name: "initial_manual_review",
        when: 'facts["synthetic.document"] == "satisfied"',
        result: {
          state: "satisfied",
          directive: "route_manual_review",
          priority: 3,
          contributing_facts: ["synthetic.document"],
          reason_codes: ["manual_review_required"],
        },
      },
    ],
  };
}

function handoff(journey, stage) {
  const credential = stage === "original" ? journey.parent : recaptureHandoff(journey);
  if (stage !== "original" && stage !== "recapture") throw new Error("invalid handoff stage");
  return { baseUrl: "/core/", ...credential };
}

function recaptureHandoff(journey) {
  const credential = required(journey.recapture, "recapture credential");
  return {
    verificationId: credential.verification_id,
    captureToken: credential.capture_token,
    outcomeToken: credential.outcome_token,
    outcomeExpiresAt: credential.outcome_token_expires_at,
    expiresAt: credential.verification_expires_at,
  };
}

async function waitForCase(journey) {
  if (journey.reviewCase !== undefined) return journey.reviewCase;
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    const queue = await runStage("list review queue", () =>
      journey.tenant.reviews.listReviewCases({ limit: 100 }),
    );
    const reviewCase = queue.data.items.find(
      (item) => item.verification_id === journey.parent.verificationId,
    );
    if (reviewCase !== undefined) {
      journey.reviewCase = reviewCase;
      return reviewCase;
    }
    await pause(250);
  }
  throw new Error("review case did not become available");
}

function caseSummary(reviewCase) {
  return {
    id: reviewCase.id,
    verificationId: reviewCase.verification_id,
    state: reviewCase.state,
    version: reviewCase.version,
    region: reviewCase.region,
    certificate: reviewCase.required_certificate,
    reason: reviewCase.reason,
  };
}

async function claimAndReadEvidence(journey) {
  let reviewCase = await waitForCase(journey);
  if (reviewCase.state === "open") {
    const claimed = await runStage("claim review case", () =>
      journey.tenant.reviews.claimReviewCase(reviewCase.id, {
        expected_version: reviewCase.version,
      }),
    );
    reviewCase = claimed.data;
    journey.reviewCase = reviewCase;
  }
  const evidence = await runStage("list review evidence", () =>
    journey.tenant.reviews.listReviewEvidence(reviewCase.id, {
      expected_version: reviewCase.version,
    }),
  );
  const item = required(evidence.data.items[0], "review evidence");
  const grant = await runStage("issue review evidence grant", () =>
    journey.tenant.reviews.issueReviewEvidenceGrant(
      reviewCase.id,
      { expected_version: reviewCase.version, evidence_id: item.id },
      { idempotencyKey: createIdempotencyKey("review_demo_evidence") },
    ),
  );
  const content = await runStage("read review evidence", () =>
    journey.tenant.reviews.readReviewEvidence(grant.data.grant_id),
  );
  journey.grantID = grant.data.grant_id;
  return content.data;
}

async function requestRecapture(journey) {
  if (journey.recapture !== undefined) return journey.recapture;
  const reviewCase = required(journey.reviewCase, "claimed review case");
  const grantID = required(journey.grantID, "redeemed evidence grant");
  const resolved = await runStage("request more subject input", () =>
    journey.tenant.reviews.submitReviewFinding(reviewCase.id, {
      expected_version: reviewCase.version,
      resolution: "request_input",
      reason_code: "image_unclear",
      grant_ids: [grantID],
    }),
  );
  journey.reviewCase = resolved.data;
  const idempotencyKey = createIdempotencyKey("review_demo_recapture");
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    try {
      const created = await journey.tenant.reviews.createReviewRecapture(
        resolved.data.id,
        { expected_version: resolved.data.version },
        { idempotencyKey },
      );
      journey.recapture = created.data;
      await declareAuthority({
        tenant: journey.tenant,
        verificationID: created.data.verification_id,
        verificationExpiresAt: created.data.verification_expires_at,
        notice: journey.notice,
        region: journey.region,
        idempotencyPrefix: "review_demo_child_authority",
      });
      return created.data;
    } catch (error) {
      if (error?.status !== 409 && error?.status !== 503) throw error;
      await pause(250);
    }
  }
  throw new Error("review recapture did not become available");
}

async function childResult(journey) {
  const reviewCase = required(journey.reviewCase, "review case");
  const listed = await runStage("read recapture result", () =>
    journey.tenant.reviews.listReviewRecaptures(reviewCase.id),
  );
  const item = listed.data.items.at(-1);
  if (item === undefined || item.decision_id === undefined) {
    return { complete: false, state: item?.state ?? "collecting" };
  }
  journey.childResult = item;
  return {
    complete: true,
    state: item.state,
    outcome: item.outcome,
    decisionId: item.decision_id,
    followUpRequired: item.follow_up_required,
  };
}

async function completeReview(journey) {
  const reviewCase = required(journey.reviewCase, "review case");
  const result = journey.childResult ?? (await childResult(journey));
  if (result.complete === false) throw new Error("child result is not complete");
  const decisionID = result.decision_id ?? result.decisionId;
  const caseVersion = result.case_version ?? reviewCase.version;
  await runStage("acknowledge recapture outcome", () =>
    journey.tenant.reviews.acknowledgeReviewRecapture(
      reviewCase.id,
      { expected_version: caseVersion, decision_id: decisionID },
      { idempotencyKey: createIdempotencyKey("review_demo_acknowledgement") },
    ),
  );
  await runStage("request parent re-evaluation", () =>
    journey.tenant.reviews.reevaluateReviewRecapture(
      reviewCase.id,
      { expected_version: caseVersion, decision_id: decisionID },
      { idempotencyKey: createIdempotencyKey("review_demo_reevaluation") },
    ),
  );
  return waitForParentOutcome(journey);
}

async function waitForParentOutcome(journey) {
  const outcome = new OutcomeClient({
    baseUrl: journey.coreUrl,
    outcomeToken: journey.parent.outcomeToken,
  });
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    const current = await runStage("read parent outcome", () => outcome.getOutcome());
    if (current.data.state === "verified") {
      return { state: current.data.state, sessionVersion: current.data.sessionVersion };
    }
    await pause(250);
  }
  throw new Error("parent review outcome did not complete");
}

function writeJSON(response, status, body) {
  if (response.writableEnded) return;
  response.statusCode = status;
  response.setHeader("Content-Type", "application/json; charset=utf-8");
  response.end(JSON.stringify(body));
}

function required(value, label) {
  if (value === undefined || value === null || value === "") throw new Error(`${label} is missing`);
  return value;
}

function pause(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

function safeErrorMessage(error) {
  if (!(error instanceof Error)) return "unknown error";
  const cause = error.cause instanceof Error ? ` cause=${error.cause.message}` : "";
  const code = typeof error.code === "string" ? ` code=${error.code}` : "";
  const status = typeof error.status === "number" ? ` status=${error.status}` : "";
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
