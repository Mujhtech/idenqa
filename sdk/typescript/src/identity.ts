import type { components } from "./generated/openapi.js";
import { JSONTransport } from "./transport.js";
import type { SDKResponse } from "./types.js";

export type IdentitySubjectID = components["schemas"]["IdentitySubjectID"];
export type IdentityRecordID = components["schemas"]["IdentityRecordID"];
export type IdentityValue = components["schemas"]["IdentityValue"];
export type IdentitySubject = components["schemas"]["IdentitySubject"];
export type IdentityIdentifierInput = components["schemas"]["IdentityIdentifierInput"];
export type IdentityIdentifier = components["schemas"]["IdentityIdentifier"];
export type IdentityRecordInput = components["schemas"]["IdentityRecordInput"];
export type IdentityRecord = components["schemas"]["IdentityRecord"];
export type IdentitySourceBinding = components["schemas"]["IdentitySourceBinding"];
export type IdentityRequirement = components["schemas"]["IdentityRequirement"];
export type IdentityConfiguration = components["schemas"]["IdentityConfiguration"];
export type IdentityFinding = components["schemas"]["IdentityFinding"];
export type IdentityReceipt = components["schemas"]["IdentityReceipt"];
export type IdentityResult = components["schemas"]["IdentityResult"];
export type IdentityIdentifierLookup = components["schemas"]["IdentityIdentifierLookup"];

export interface IdentityRequestOptions {
  readonly signal?: AbortSignal;
}
export interface IdentityMutationOptions extends IdentityRequestOptions {
  readonly idempotencyKey: string;
}
export interface IdentityListOptions extends IdentityRequestOptions {
  readonly after?: string;
  readonly limit?: number;
  readonly reveal?: boolean;
  readonly current?: boolean;
  readonly kind?: "observation" | "fact" | "claim" | "identifier";
}
/** Persistent tenant subjects and immutable identity provenance. Use only from a trusted backend. */
export class IdentityClient {
  constructor(
    private readonly transport: JSONTransport,
    private readonly token: string,
  ) {}
  createSubject(
    externalReference: string | undefined,
    options: IdentityMutationOptions,
  ): Promise<SDKResponse<IdentityResult>> {
    return this.mutate("POST", "/v1/subjects", { external_reference: externalReference }, options);
  }
  updateSubject(
    subjectId: string,
    input: {
      readonly expected_version: number;
      readonly external_reference?: string;
      readonly state?: "active" | "suspended";
    },
    options: IdentityMutationOptions,
  ): Promise<SDKResponse<IdentityResult>> {
    return this.mutate("PUT", this.subjectPath(subjectId), input, options);
  }
  deleteSubject(
    subjectId: string,
    expectedVersion: number,
    options: IdentityMutationOptions,
  ): Promise<SDKResponse<IdentityResult>> {
    return this.mutate(
      "DELETE",
      this.subjectPath(subjectId),
      { expected_version: expectedVersion },
      options,
    );
  }
  getSubject(
    subjectId: string,
    options: IdentityListOptions = {},
  ): Promise<SDKResponse<IdentityResult>> {
    return this.read(this.subjectPath(subjectId), options);
  }
  listSubjects(options: IdentityListOptions = {}): Promise<SDKResponse<IdentityResult>> {
    return this.read("/v1/subjects", options);
  }
  linkVerification(
    subjectId: string,
    verificationId: string,
    expectedVersion: number,
    options: IdentityMutationOptions,
  ): Promise<SDKResponse<IdentityResult>> {
    if (!/^ver_[0-9A-HJKMNP-TV-Z]{26}$/.test(verificationId))
      throw new TypeError("A verification ID is required.");
    return this.mutate(
      "PUT",
      `${this.subjectPath(subjectId)}/verifications/${verificationId}`,
      { expected_version: expectedVersion },
      options,
    );
  }
  listVerifications(
    subjectId: string,
    options: IdentityListOptions = {},
  ): Promise<SDKResponse<IdentityResult>> {
    return this.read(`${this.subjectPath(subjectId)}/verifications`, options);
  }
  addRecord(
    subjectId: string,
    record: IdentityRecordInput,
    expectedVersion: number,
    options: IdentityMutationOptions,
  ): Promise<SDKResponse<IdentityResult>> {
    return this.mutate(
      "POST",
      `${this.subjectPath(subjectId)}/records`,
      { record, expected_version: expectedVersion },
      options,
    );
  }
  listRecords(
    subjectId: string,
    options: IdentityListOptions = {},
  ): Promise<SDKResponse<IdentityResult>> {
    return this.read(`${this.subjectPath(subjectId)}/records`, options);
  }
  getRecord(
    subjectId: string,
    recordId: string,
    options: IdentityListOptions = {},
  ): Promise<SDKResponse<IdentityResult>> {
    if (!/^(obs|fct|clm|idi)_[0-9A-HJKMNP-TV-Z]{26}$/.test(recordId))
      throw new TypeError("An identity record ID is required.");
    return this.read(`${this.subjectPath(subjectId)}/records/${recordId}`, options);
  }
  rebuildProjection(
    subjectId: string,
    expectedVersion: number,
    options: IdentityMutationOptions,
  ): Promise<SDKResponse<IdentityResult>> {
    return this.mutate(
      "POST",
      `${this.subjectPath(subjectId)}/projection/rebuild`,
      { expected_version: expectedVersion },
      options,
    );
  }
  lookupExternalReference(
    externalReference: string,
    options: IdentityListOptions = {},
  ): Promise<SDKResponse<IdentityResult>> {
    return this.lookup(
      "/v1/subjects/lookup",
      { external_reference: externalReference, after: options.after, limit: options.limit },
      options,
    );
  }
  lookupIdentifier(
    identifier: IdentityIdentifierLookup,
    options: IdentityListOptions = {},
  ): Promise<SDKResponse<IdentityResult>> {
    return this.lookup(
      "/v1/identity/identifiers/lookup",
      { identifier, after: options.after, limit: options.limit },
      options,
    );
  }
  configure(
    configuration: IdentityConfiguration,
    expectedVersion: number,
    options: IdentityMutationOptions,
  ): Promise<SDKResponse<IdentityResult>> {
    return this.mutate(
      "PUT",
      "/v1/identity/configuration",
      { configuration, expected_version: expectedVersion },
      options,
    );
  }
  getConfiguration(options: IdentityRequestOptions = {}): Promise<SDKResponse<IdentityResult>> {
    return this.read("/v1/identity/configuration", options);
  }
  getConfigurationRevision(
    version: number,
    options: IdentityRequestOptions = {},
  ): Promise<SDKResponse<IdentityResult>> {
    if (!Number.isSafeInteger(version) || version < 1)
      throw new TypeError("A positive configuration revision is required.");
    return this.read(`/v1/identity/configuration/revisions/${version}`, options);
  }
  getReceipt(
    digest: string,
    options: IdentityRequestOptions = {},
  ): Promise<SDKResponse<IdentityResult>> {
    if (!/^[0-9a-f]{64}$/.test(digest))
      throw new TypeError("An identity receipt digest is required.");
    return this.read(`/v1/identity/receipts/${digest}`, options);
  }
  private subjectPath(subjectId: string): string {
    if (!/^sub_[0-9A-HJKMNP-TV-Z]{26}$/.test(subjectId))
      throw new TypeError("A subject ID is required.");
    return `/v1/subjects/${subjectId}`;
  }
  private read(path: string, options: IdentityListOptions): Promise<SDKResponse<IdentityResult>> {
    const query = new URLSearchParams();
    for (const key of ["after", "limit", "reveal", "current", "kind"] as const) {
      const value = options[key];
      if (value !== undefined) query.set(key, String(value));
    }
    return this.transport.request<IdentityResult>({
      method: "GET",
      path: `${path}${query.size ? `?${query}` : ""}`,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
  private lookup(
    path: string,
    body: unknown,
    options: IdentityRequestOptions,
  ): Promise<SDKResponse<IdentityResult>> {
    return this.transport.request<IdentityResult>({
      method: "POST",
      path,
      body,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
  private mutate(
    method: "POST" | "PUT" | "DELETE",
    path: string,
    body: unknown,
    options: IdentityMutationOptions,
  ): Promise<SDKResponse<IdentityResult>> {
    if (
      !/^[\x21-\x7e]{1,200}$/.test(options.idempotencyKey) ||
      /["\\]/.test(options.idempotencyKey)
    )
      throw new TypeError("A bounded idempotency key is required.");
    return this.transport.request<IdentityResult>({
      method,
      path,
      body,
      bearerToken: this.token,
      headers: { "Idempotency-Key": JSON.stringify(options.idempotencyKey) },
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
}
