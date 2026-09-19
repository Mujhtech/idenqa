import type { components } from "./generated/openapi.js";

type GeneratedCaptureProfileDocument = components["schemas"]["profile.schema"];

// openapi-typescript currently emits the JSON Schema `$defs` keyword as if it
// were an instance property when following the external profile schema. `$defs`
// describes the schema and is never part of a capture-profile document.
export type WireCaptureProfileDocument = Omit<GeneratedCaptureProfileDocument, "$defs">;
export type WireCaptureProfileWrite = Omit<
  components["schemas"]["CaptureProfileWrite"],
  "document"
> & {
  readonly document: WireCaptureProfileDocument;
};
export type WireCaptureProfileSupersede = Omit<
  components["schemas"]["CaptureProfileSupersede"],
  "document"
> & {
  readonly document: WireCaptureProfileDocument;
};
export type WireCaptureProfile = components["schemas"]["CaptureProfile"];
export type WireCaptureProfileMutation = components["schemas"]["CaptureProfileMutation"];
export type WireCaptureProfileRevision = Omit<
  components["schemas"]["CaptureProfileRevision"],
  "document"
> & {
  readonly document: WireCaptureProfileDocument;
};
export type WireCaptureProfileList = components["schemas"]["CaptureProfileList"];
export type WireCaptureProfileValidation = components["schemas"]["CaptureProfileValidation"];
export type WireVerificationCreate = components["schemas"]["VerificationCreate"];
export type WireVerificationSession = Omit<
  components["schemas"]["VerificationSession"],
  "requirements"
> & {
  readonly requirements: WireCaptureProfileDocument;
};
export type WireVerificationCreated = Omit<
  components["schemas"]["VerificationCreated"],
  "session"
> & {
  readonly session: WireVerificationSession;
};
export type WireNoticeVersionCreate = components["schemas"]["NoticeVersionCreate"];
export type WireNoticeVersion = components["schemas"]["NoticeVersion"];
export type WireProcessingAuthorityDeclare = components["schemas"]["ProcessingAuthorityDeclare"];
export type WireProcessingAuthority = components["schemas"]["ProcessingAuthority"];
export type WireSubjectResponseCreate = components["schemas"]["SubjectResponseCreate"];
export type WireSubjectResponse = components["schemas"]["SubjectResponse"];
export type WireCaptureAuthoritySnapshot = components["schemas"]["CaptureAuthoritySnapshot"];
export type WireCaptureProgress = components["schemas"]["CaptureProgress"];
export type WireCaptureOutcome = components["schemas"]["CaptureOutcome"];
export type WireCaptureConnection = components["schemas"]["CaptureConnection"];
export type WireEvidenceUploadCreate = components["schemas"]["EvidenceUploadCreate"];
export type WireEvidenceUpload = components["schemas"]["EvidenceUpload"];
export type WirePolicyDecisionReport = components["schemas"]["PolicyDecisionReport"];
export type WirePolicyDecisionBundle = components["schemas"]["PolicyDecisionBundle"];
export type WireProblem = components["schemas"]["Problem"];

export type WireVerificationCancellation = components["schemas"]["VerificationCancellation"];
export type WireVerificationResumed = Omit<
  components["schemas"]["VerificationResumed"],
  "session"
> & {
  readonly session: WireVerificationSession;
};

export type WireWebhookEndpoint = components["schemas"]["WebhookEndpoint"];
export type WireWebhookDelivery = components["schemas"]["WebhookDelivery"];
export type WireWebhookAttempt = components["schemas"]["WebhookAttempt"];
export type WireWebhookEndpointMutation = components["schemas"]["WebhookEndpointMutation"];
export type WireWebhookDeliveryMutation = components["schemas"]["WebhookDeliveryMutation"];
export type WireWebhookEndpointList = components["schemas"]["WebhookEndpointList"];
export type WireWebhookDeliveryList = components["schemas"]["WebhookDeliveryList"];
export type WireWebhookAttemptList = components["schemas"]["WebhookAttemptList"];

export type WirePolicySummary = components["schemas"]["PolicySummary"];
export type WirePolicyRevisionInfo = components["schemas"]["PolicyRevisionInfo"];
export type WirePolicyRevisionDocument = components["schemas"]["PolicyRevisionDocument"];
export type WirePolicyActivationInfo = components["schemas"]["PolicyActivationInfo"];
export type WirePolicyValidation = components["schemas"]["PolicyValidation"];
export type WirePolicyMutation = components["schemas"]["PolicyMutation"];
export type WirePolicyList = components["schemas"]["PolicyList"];
export type WirePolicyRevisionList = components["schemas"]["PolicyRevisionList"];
export type WirePolicyActivationList = components["schemas"]["PolicyActivationList"];
export type WirePolicyDefinition = components["schemas"]["PolicyDefinition"];
export type WirePolicyDocument = components["schemas"]["PolicyDocument"];
