import type { components } from "./generated/openapi.js";
import { JSONTransport } from "./transport.js";
import type { SDKResponse } from "./types.js";

export type AssuranceProfile = components["schemas"]["AssuranceProfile"];
export type AssuranceSelection = components["schemas"]["AssuranceSelection"];
export type AssuranceRequirement = components["schemas"]["AssuranceRequirement"];
export type AssuranceMapping = components["schemas"]["AssuranceMapping"];
export type AssuranceSummary = components["schemas"]["AssuranceSummary"];
export type AssuranceResource = components["schemas"]["AssuranceResource"];
export type AssuranceCapabilityCatalog = components["schemas"]["AssuranceCapabilityCatalog"];
export type DecisionContext = components["schemas"]["DecisionContext"];
export interface AssuranceRequestOptions {
  readonly signal?: AbortSignal;
}
export interface AssuranceMutationOptions extends AssuranceRequestOptions {
  readonly idempotencyKey: string;
}
export interface AssuranceListOptions extends AssuranceRequestOptions {
  readonly after?: string;
  readonly limit?: number;
}

/** Tenant assurance administration. Use only from a trusted backend. */
export class AssuranceClient {
  constructor(
    private readonly transport: JSONTransport,
    private readonly token: string,
  ) {}
  capabilities(
    options: AssuranceRequestOptions = {},
  ): Promise<SDKResponse<AssuranceCapabilityCatalog>> {
    return this.transport.request<AssuranceCapabilityCatalog>({
      method: "GET",
      path: "/v1/assurance-capabilities",
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
  listProfiles(options: AssuranceListOptions = {}): Promise<SDKResponse<AssuranceResource>> {
    if (
      options.limit !== undefined &&
      (!Number.isInteger(options.limit) || options.limit < 1 || options.limit > 100)
    )
      throw new TypeError("Limit must be between 1 and 100.");
    if (options.after !== undefined && options.after.length > 80)
      throw new TypeError("Assurance cursor exceeds its limit.");
    const query = new URLSearchParams();
    if (options.after !== undefined) query.set("after", options.after);
    if (options.limit !== undefined) query.set("limit", String(options.limit));
    return this.read(`/v1/assurance-profiles${query.size === 0 ? "" : `?${query}`}`, options);
  }
  getProfile(
    name: string,
    revision: number,
    options: AssuranceRequestOptions = {},
  ): Promise<SDKResponse<AssuranceResource>> {
    if (
      !/^[a-z][a-z0-9._:-]{0,63}$/.test(name) ||
      !Number.isInteger(revision) ||
      revision < 1 ||
      revision > 4294967295
    )
      throw new TypeError("An exact assurance name and positive revision are required.");
    return this.read(
      `/v1/assurance-profiles/${encodeURIComponent(name)}/revisions/${revision}`,
      options,
    );
  }
  publishProfile(
    profile: AssuranceProfile,
    options: AssuranceMutationOptions,
  ): Promise<SDKResponse<AssuranceResource>> {
    return this.mutate("POST", "/v1/assurance-profiles", profile, options);
  }
  validateProfile(
    profile: AssuranceProfile,
    options: AssuranceRequestOptions = {},
  ): Promise<SDKResponse<AssuranceResource>> {
    return this.transport.request<AssuranceResource>({
      method: "POST",
      path: "/v1/assurance-profiles/validate",
      body: profile,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
  getPolicySelection(
    policyId: string,
    options: AssuranceRequestOptions = {},
  ): Promise<SDKResponse<AssuranceResource>> {
    return this.read(`/v1/policies/${identifier(policyId, "pol")}/assurance`, options);
  }
  assignPolicy(
    policyId: string,
    selection: AssuranceSelection | null,
    expectedVersion: number,
    options: AssuranceMutationOptions,
  ): Promise<SDKResponse<AssuranceResource>> {
    if (!Number.isSafeInteger(expectedVersion) || expectedVersion < 0)
      throw new TypeError("A non-negative expected assignment version is required.");
    return this.mutate(
      "PUT",
      `/v1/policies/${identifier(policyId, "pol")}/assurance`,
      { selection, expected_version: expectedVersion },
      options,
    );
  }
  getVerificationSelection(
    verificationId: string,
    options: AssuranceRequestOptions = {},
  ): Promise<SDKResponse<AssuranceResource>> {
    return this.read(`/v1/verifications/${identifier(verificationId, "ver")}/assurance`, options);
  }
  private read(
    path: string,
    options: AssuranceRequestOptions,
  ): Promise<SDKResponse<AssuranceResource>> {
    return this.transport.request<AssuranceResource>({
      method: "GET",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
  private mutate(
    method: "POST" | "PUT",
    path: string,
    body: unknown,
    options: AssuranceMutationOptions,
  ): Promise<SDKResponse<AssuranceResource>> {
    if (
      !/^[\x21-\x7e]{1,200}$/.test(options.idempotencyKey) ||
      /["\\]/.test(options.idempotencyKey)
    )
      throw new TypeError("A bounded idempotency key is required.");
    return this.transport.request<AssuranceResource>({
      method,
      path,
      body,
      bearerToken: this.token,
      headers: { "Idempotency-Key": JSON.stringify(options.idempotencyKey) },
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
}
function identifier(value: string, prefix: "pol" | "ver"): string {
  if (!new RegExp(`^${prefix}_[0-9A-HJKMNP-TV-Z]{26}$`).test(value))
    throw new TypeError("Invalid assurance resource identifier.");
  return encodeURIComponent(value);
}
