import { describe, expect, it } from "vitest";

import {
  CaptureClient,
  IdenqaClient,
  OutcomeClient,
  createIdempotencyKey,
  type CaptureProfileDocument,
} from "../src/index.js";

const baseUrl = process.env.IDENQA_SDK_CONFORMANCE_BASE_URL;
const apiKey = process.env.IDENQA_SDK_CONFORMANCE_API_KEY;
const conformance = baseUrl !== undefined && apiKey !== undefined ? describe : describe.skip;

conformance("running Idenqa Core", () => {
  it("publishes a profile, replays session creation, and bootstraps capture", async () => {
    if (baseUrl === undefined || apiKey === undefined)
      throw new Error("conformance config missing");
    const tenant = new IdenqaClient({ baseUrl, apiKey });
    const profileKey = createIdempotencyKey("profile");
    const createdProfile = await tenant.captureProfiles.create(
      { name: "SDK conformance selfie", document: selfieUploadDocument },
      { idempotencyKey: profileKey },
    );
    expect(createdProfile.etag).toBe('"1"');

    const publishedProfile = await tenant.captureProfiles.publish(createdProfile.data.profileId, {
      etag: required(createdProfile.etag, "created profile ETag"),
      idempotencyKey: createIdempotencyKey("publish"),
    });
    expect(publishedProfile.data.state).toBe("active");

    const verificationKey = createIdempotencyKey("verification");
    const input = {
      captureProfileId: createdProfile.data.profileId,
      policyId: "pol_01ARZ3NDEKTSV4RRFFQ69G5FAV",
    } as const;
    const first = await tenant.verifications.create(input, { idempotencyKey: verificationKey });
    const replay = await tenant.verifications.create(input, { idempotencyKey: verificationKey });
    expect(replay.data).toEqual(first.data);

    const tenantSnapshot = await tenant.verifications.get(first.data.session.id);
    expect(tenantSnapshot.data.requirements).toEqual(selfieUploadDocument);
    expect(tenantSnapshot.data.profileRevision).toBe(publishedProfile.data.publishedRevision);

    const capture = new CaptureClient({ baseUrl, captureToken: first.data.captureToken });
    const captureSnapshot = await capture.getSession();
    expect(captureSnapshot.data).toEqual(tenantSnapshot.data);
    expect(captureSnapshot.requestId).toMatch(/^req_/);

    const outcome = new OutcomeClient({ baseUrl, outcomeToken: first.data.outcomeToken });
    const subjectOutcome = await outcome.getOutcome();
    expect(subjectOutcome.data).toMatchObject({
      verificationId: first.data.session.id,
      state: "capture_required",
      sessionVersion: 1,
    });
    expect(subjectOutcome.requestId).toMatch(/^req_/);
  });
});

const selfieUploadDocument: CaptureProfileDocument = {
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
      acquisition: { strategy: "any_of", methods: ["idenqa.method.file_upload"] },
      required_assurances: [],
      constraints: [],
      fallbacks: [],
    },
  ],
};

function required<T>(value: T | undefined, label: string): T {
  if (value === undefined) throw new Error(`${label} is missing`);
  return value;
}
