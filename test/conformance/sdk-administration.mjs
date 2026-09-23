import assert from "node:assert/strict";
import { IdenqaClient } from "../../sdk/typescript/dist/index.js";

const client = new IdenqaClient({
  baseUrl: process.env.IDENQA_SDK_BASE,
  apiKey: process.env.IDENQA_SDK_KEY,
});
const evidence = await client.evidence.get(process.env.IDENQA_SDK_EVIDENCE);
assert.equal(evidence.data.verification_id, process.env.IDENQA_SDK_VERIFICATION);
assert.ok(evidence.requestId);
assert.ok((await client.evidence.lifecycle(evidence.data.id)).data.data.length);
assert.equal((await client.consents.get(process.env.IDENQA_SDK_CONSENT)).data.action, "consent");
const assessments = await client.proposals.listImpactAssessments({ limit: 1 });
assert.equal(assessments.data.data.length, 1);
assert.equal(
  (await client.proposals.getImpactAssessment(assessments.data.data[0].id)).data.risk_level,
  "high",
);
const first = await client.privacy.listRequests({ limit: 1 });
assert.equal(first.data.data.length, 1);
assert.ok(first.data.page.has_more);
const next = await client.privacy.listRequests({ limit: 1, cursor: first.data.page.next_cursor });
assert.notEqual(next.data.data[0].id, first.data.data[0].id);
assert.equal(
  (await client.privacy.getRequest(first.data.data[0].id)).data.id,
  first.data.data[0].id,
);
const processor = await client.privacy.createProcessor({
  name: "TypeScript SDK fixture",
  role: "processor",
  purpose: "sdk.proof",
  data_classes: ["workflow_metadata"],
  regions: ["tenant.region.ng"],
  transfer_mechanism: "tenant.contract",
  expected_version: 0,
});
assert.equal(processor.data.version, 1);
assert.equal(
  (await client.privacy.getProcessor(processor.data.id)).data.name,
  "TypeScript SDK fixture",
);
assert.ok((await client.privacy.listProcessors()).data.data.length >= 2);
assert.equal(
  (await client.privacy.listRestrictions({ subjectId: evidence.data.subject_id })).data.data[0]
    .state,
  "lifted",
);
assert.ok((await client.privacy.listDisclosures()).data.data.length);
await client.privacy.listDeletions({ aggregateId: process.env.IDENQA_SDK_VERIFICATION });
await client.privacy.retention(process.env.IDENQA_SDK_VERIFICATION);
assert.equal(
  (await client.decisions.history(process.env.IDENQA_SDK_VERIFICATION)).data.data.length,
  0,
);
