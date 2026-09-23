import type { components } from "./generated/openapi.js";
import { JSONTransport } from "./transport.js";
import type { IdempotentRequestOptions, RequestOptions, SDKResponse } from "./types.js";

export type EvidenceMetadata = components["schemas"]["EvidenceMetadata"];
export type EvidenceLifecycleList = components["schemas"]["EvidenceLifecycleList"];
export type EvidenceAccessGrant = components["schemas"]["EvidenceAccessGrant"];
export type EvidenceAccessGrantCreate = components["schemas"]["EvidenceAccessGrantCreate"];
export type ConsentReceipt = components["schemas"]["SubjectResponse"];
export type PrivacyRequestCreate = components["schemas"]["PrivacyRequestCreate"];
export type PrivacyRequestSummary = components["schemas"]["PrivacyRequestSummary"];
export type PrivacyRequestStatus = components["schemas"]["PrivacyRequestStatus"];
export type PrivacyRequestList = components["schemas"]["PrivacyRequestList"];
export type PrivacyRequestDecision = components["schemas"]["PrivacyRequestDecision"];
export type PrivacyRestriction = components["schemas"]["PrivacyRestriction"];
export type PrivacyRestrictionList = components["schemas"]["PrivacyRestrictionList"];
export type PrivacyRestrictionLift = components["schemas"]["PrivacyRestrictionLift"];
export type PrivacyDisclosure = components["schemas"]["PrivacyDisclosure"];
export type PrivacyDisclosureCreate = components["schemas"]["PrivacyDisclosureCreate"];
export type PrivacyDisclosureList = components["schemas"]["PrivacyDisclosureList"];
export type PrivacyProcessor = components["schemas"]["PrivacyProcessor"];
export type PrivacyProcessorPut = components["schemas"]["PrivacyProcessorPut"];
export type PrivacyProcessorList = components["schemas"]["PrivacyProcessorList"];
export type PrivacyDeletionStatus = components["schemas"]["PrivacyDeletionStatus"];
export type PrivacyDeletionList = components["schemas"]["PrivacyDeletionList"];
export type PrivacyRetentionResolution = components["schemas"]["PrivacyRetentionResolution"];

export interface ResourceListOptions extends RequestOptions {
  readonly limit?: number;
  readonly cursor?: string;
}
export interface PrivacyRequestListOptions extends ResourceListOptions {
  readonly state?: components["schemas"]["PrivacyRequestState"];
  readonly type?: components["schemas"]["PrivacyRequestType"];
  readonly subjectId?: string;
}

/** Internal transport adapter. Public resource documents retain their wire field names. */
class ResourceClient {
  constructor(
    private readonly transport: JSONTransport,
    private readonly token: string,
  ) {}

  protected read<T>(path: string, options: RequestOptions): Promise<SDKResponse<T>> {
    return this.send<T>("GET", path, undefined, options);
  }

  protected send<T>(
    method: "GET" | "POST" | "PUT",
    path: string,
    body: unknown,
    options: RequestOptions,
    headers?: Readonly<Record<string, string>>,
  ): Promise<SDKResponse<T>> {
    return this.transport.request<T>({
      method,
      path,
      bearerToken: this.token,
      ...(body === undefined ? {} : { body }),
      ...(headers === undefined ? {} : { headers }),
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
}

/** Tenant-only safe metadata and purpose-bound processing grants; never evidence bytes. */
export class EvidenceClient extends ResourceClient {
  get(evidenceId: string, options: RequestOptions = {}): Promise<SDKResponse<EvidenceMetadata>> {
    return this.read(`v1/evidence/${resourceID(evidenceId, "evd")}`, options);
  }
  lifecycle(
    evidenceId: string,
    options: RequestOptions & { readonly limit?: number } = {},
  ): Promise<SDKResponse<EvidenceLifecycleList>> {
    return this.read(
      `v1/evidence/${resourceID(evidenceId, "evd")}/lifecycle${resourceQuery({ limit: options.limit })}`,
      options,
    );
  }
  getGrant(
    grantId: string,
    options: RequestOptions = {},
  ): Promise<SDKResponse<EvidenceAccessGrant>> {
    return this.read(`v1/evidence-access-grants/${resourceID(grantId, "grt")}`, options);
  }
  createGrant(
    evidenceId: string,
    input: EvidenceAccessGrantCreate,
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<EvidenceAccessGrant>> {
    return this.send(
      "POST",
      `v1/evidence/${resourceID(evidenceId, "evd")}/access-grants`,
      input,
      options,
      mutationHeaders(options),
    );
  }
  revokeGrant(
    grantId: string,
    reason: string,
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<EvidenceAccessGrant>> {
    return this.send(
      "POST",
      `v1/evidence-access-grants/${resourceID(grantId, "grt")}/revoke`,
      { reason },
      options,
      mutationHeaders(options),
    );
  }
}

/** Tenant inspection and withdrawal. Creation remains on the capture-token client. */
export class ConsentsClient extends ResourceClient {
  get(consentId: string, options: RequestOptions = {}): Promise<SDKResponse<ConsentReceipt>> {
    return this.read(`v1/consent-receipts/${resourceID(consentId, "ack")}`, options);
  }
  revoke(
    consentId: string,
    reason: string,
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<ConsentReceipt>> {
    return this.send(
      "POST",
      `v1/consent-receipts/${resourceID(consentId, "ack")}/revoke`,
      { reason },
      options,
      mutationHeaders(options),
    );
  }
}

/** Tenant privacy administration. No automatic retries or invented idempotency headers. */
export class PrivacyClient extends ResourceClient {
  createRequest(
    input: PrivacyRequestCreate,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PrivacyRequestSummary>> {
    return this.send("POST", "v1/privacy-requests", input, options);
  }
  getRequest(
    requestId: string,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PrivacyRequestStatus>> {
    return this.read(`v1/privacy-requests/${resourceID(requestId, "prq")}`, options);
  }
  listRequests(options: PrivacyRequestListOptions = {}): Promise<SDKResponse<PrivacyRequestList>> {
    return this.read(
      `v1/privacy-requests${resourceQuery({ limit: options.limit, cursor: options.cursor, state: options.state, type: options.type, subject_id: options.subjectId })}`,
      options,
    );
  }
  approveRequest(
    requestId: string,
    input: PrivacyRequestDecision,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PrivacyRequestStatus>> {
    return this.send(
      "POST",
      `v1/privacy-requests/${resourceID(requestId, "prq")}/approve`,
      input,
      options,
    );
  }
  denyRequest(
    requestId: string,
    input: PrivacyRequestDecision,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PrivacyRequestStatus>> {
    return this.send(
      "POST",
      `v1/privacy-requests/${resourceID(requestId, "prq")}/deny`,
      input,
      options,
    );
  }
  withdrawRequest(
    requestId: string,
    expectedVersion: number,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PrivacyRequestStatus>> {
    return this.send(
      "POST",
      `v1/privacy-requests/${resourceID(requestId, "prq")}/withdraw`,
      { expected_version: positiveVersion(expectedVersion) },
      options,
    );
  }
  executeRequest(
    requestId: string,
    expectedVersion: number,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PrivacyRequestStatus>> {
    return this.send(
      "POST",
      `v1/privacy-requests/${resourceID(requestId, "prq")}/execute`,
      { expected_version: positiveVersion(expectedVersion) },
      options,
    );
  }
  listRestrictions(
    options: ResourceListOptions & { readonly subjectId?: string } = {},
  ): Promise<SDKResponse<PrivacyRestrictionList>> {
    return this.read(
      `v1/privacy-restrictions${resourceQuery({ limit: options.limit, cursor: options.cursor, subject_id: options.subjectId })}`,
      options,
    );
  }
  liftRestriction(
    restrictionId: string,
    input: PrivacyRestrictionLift,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PrivacyRestriction>> {
    return this.send(
      "POST",
      `v1/privacy-restrictions/${resourceID(restrictionId, "prs")}/lift`,
      input,
      options,
    );
  }
  createDisclosure(
    input: PrivacyDisclosureCreate,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PrivacyDisclosure>> {
    return this.send("POST", "v1/privacy-disclosures", input, options);
  }
  listDisclosures(
    options: ResourceListOptions & { readonly requestId?: string } = {},
  ): Promise<SDKResponse<PrivacyDisclosureList>> {
    return this.read(
      `v1/privacy-disclosures${resourceQuery({ limit: options.limit, cursor: options.cursor, request_id: options.requestId })}`,
      options,
    );
  }
  createProcessor(
    input: PrivacyProcessorPut,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PrivacyProcessor>> {
    return this.send("POST", "v1/privacy-processors", input, options);
  }
  getProcessor(
    processorId: string,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PrivacyProcessor>> {
    return this.read(`v1/privacy-processors/${resourceID(processorId, "prc")}`, options);
  }
  updateProcessor(
    processorId: string,
    input: PrivacyProcessorPut,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PrivacyProcessor>> {
    return this.send(
      "PUT",
      `v1/privacy-processors/${resourceID(processorId, "prc")}`,
      input,
      options,
    );
  }
  listProcessors(options: ResourceListOptions = {}): Promise<SDKResponse<PrivacyProcessorList>> {
    return this.read(
      `v1/privacy-processors${resourceQuery({ limit: options.limit, cursor: options.cursor })}`,
      options,
    );
  }
  listDeletions(
    options: ResourceListOptions & { readonly aggregateId?: string } = {},
  ): Promise<SDKResponse<PrivacyDeletionList>> {
    return this.read(
      `v1/deletions${resourceQuery({ limit: options.limit, cursor: options.cursor, aggregate_id: options.aggregateId })}`,
      options,
    );
  }
  getDeletion(
    deletionId: string,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PrivacyDeletionStatus>> {
    return this.read(`v1/deletions/${resourceID(deletionId, "del")}`, options);
  }
  retention(
    aggregateId: string,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PrivacyRetentionResolution>> {
    if (aggregateId.length === 0) throw new TypeError("An aggregate identifier is required.");
    return this.read(
      `v1/retention/resolutions${resourceQuery({ aggregate_id: aggregateId })}`,
      options,
    );
  }
}

/** @internal */
export function resourceID(value: string, prefix: string): string {
  if (!new RegExp(`^${prefix}_[0-9A-HJKMNP-TV-Z]{26}$`).test(value))
    throw new TypeError(`A valid ${prefix} identifier is required.`);
  return value;
}

/** @internal */
export function resourceQuery(
  values: Readonly<Record<string, string | number | undefined>>,
): string {
  const query = new URLSearchParams();
  for (const [name, value] of Object.entries(values)) {
    if (value === undefined) continue;
    if (
      name === "limit" &&
      (!Number.isSafeInteger(value) || Number(value) < 1 || Number(value) > 100)
    )
      throw new TypeError("limit must be an integer from 1 to 100.");
    query.set(name, String(value));
  }
  return query.size === 0 ? "" : `?${query.toString()}`;
}

/** @internal */
export function mutationHeaders(
  options: IdempotentRequestOptions,
): Readonly<Record<string, string>> {
  const key = options.idempotencyKey;
  if (!key || /[^\x20-\x7e]/.test(key))
    throw new TypeError("A printable idempotency key is required.");
  const encoded = JSON.stringify(key);
  if (encoded.length > 130)
    throw new TypeError("Encoded idempotency key must not exceed 130 characters.");
  return { "Idempotency-Key": encoded };
}

function positiveVersion(value: number): number {
  if (!Number.isSafeInteger(value) || value < 1)
    throw new TypeError("A positive expected version is required.");
  return value;
}
