import type { components } from "./generated/openapi.js";
import { JSONTransport } from "./transport.js";
import type { SDKResponse } from "./types.js";

export type FraudConfiguration = components["schemas"]["FraudConfiguration"];
export type FraudInput = components["schemas"]["FraudInput"];
export type FraudProposal = components["schemas"]["FraudProposal"];
export type FraudResult = components["schemas"]["FraudResult"];
export interface FraudRequestOptions {
  readonly signal?: AbortSignal;
}
export interface FraudMutationOptions extends FraudRequestOptions {
  readonly idempotencyKey: string;
}

/** Tenant backend operations. Integration values are tokenized by Core; never send API keys to capture clients. */
export class FraudClient {
  constructor(
    private readonly transport: JSONTransport,
    private readonly token: string,
  ) {}
  getConfiguration(options: FraudRequestOptions = {}): Promise<SDKResponse<FraudResult>> {
    return this.read("/v1/fraud/configuration", options);
  }
  getConfigurationRevision(
    version: number,
    options: FraudRequestOptions = {},
  ): Promise<SDKResponse<FraudResult>> {
    if (!Number.isSafeInteger(version) || version < 1)
      throw new TypeError("A positive configuration revision is required.");
    return this.read(`/v1/fraud/configuration/revisions/${version}`, options);
  }
  getProposal(
    digest: string,
    options: FraudRequestOptions = {},
  ): Promise<SDKResponse<FraudResult>> {
    if (!/^[0-9a-f]{64}$/.test(digest)) throw new TypeError("A proposal digest is required.");
    return this.read(`/v1/fraud/proposals/${digest}`, options);
  }
  getReceipt(digest: string, options: FraudRequestOptions = {}): Promise<SDKResponse<FraudResult>> {
    if (!/^[0-9a-f]{64}$/.test(digest)) throw new TypeError("A risk receipt digest is required.");
    return this.read(`/v1/fraud/receipts/${digest}`, options);
  }
  configure(
    configuration: FraudConfiguration,
    expectedVersion: number,
    options: FraudMutationOptions,
  ): Promise<SDKResponse<FraudResult>> {
    if (!Number.isSafeInteger(expectedVersion) || expectedVersion < 0)
      throw new TypeError("A non-negative configuration version is required.");
    return this.mutate(
      "PUT",
      "/v1/fraud/configuration",
      { configuration, expected_version: expectedVersion },
      options,
    );
  }
  ingest(input: FraudInput, options: FraudMutationOptions): Promise<SDKResponse<FraudResult>> {
    return this.mutate("POST", "/v1/fraud/inputs", input, options);
  }
  propose(
    proposal: FraudProposal,
    options: FraudMutationOptions,
  ): Promise<SDKResponse<FraudResult>> {
    return this.mutate("POST", "/v1/fraud/proposals", proposal, options);
  }
  private read(path: string, options: FraudRequestOptions): Promise<SDKResponse<FraudResult>> {
    return this.transport.request<FraudResult>({
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
    options: FraudMutationOptions,
  ): Promise<SDKResponse<FraudResult>> {
    if (
      !/^[\x21-\x7e]{1,200}$/.test(options.idempotencyKey) ||
      /["\\]/.test(options.idempotencyKey)
    )
      throw new TypeError("A bounded idempotency key is required.");
    return this.transport.request<FraudResult>({
      method,
      path,
      body,
      bearerToken: this.token,
      headers: { "Idempotency-Key": JSON.stringify(options.idempotencyKey) },
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
}
