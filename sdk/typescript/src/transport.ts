import {
  IdenqaAPIError,
  IdenqaProtocolError,
  IdenqaTransportError,
  type ProblemDetails,
} from "./errors.js";
import type { SDKConditionalResponse, SDKResponse } from "./types.js";
import type { WireProblem } from "./wire.js";

interface TransportOptions {
  readonly baseUrl: string | URL;
  readonly fetch?: typeof globalThis.fetch;
}

interface JSONRequest {
  readonly method: "GET" | "POST" | "PUT";
  readonly path: string;
  readonly bearerToken: string;
  readonly body?: unknown;
  readonly rawBody?: BodyInit;
  readonly headers?: Readonly<Record<string, string>>;
  readonly signal?: AbortSignal;
}

interface ConditionalJSONRequest extends JSONRequest {
  readonly allowNotModified: true;
}

export interface CanonicalJSONData<T> {
  readonly canonical: string;
  readonly document: T;
}

export class JSONTransport {
  readonly #baseUrl: URL;
  readonly #fetch: typeof globalThis.fetch;

  constructor(options: TransportOptions) {
    this.#baseUrl = normalizeBaseUrl(options.baseUrl);
    const fetchImplementation = options.fetch ?? globalThis.fetch;
    if (typeof fetchImplementation !== "function") {
      throw new TypeError("A Fetch-compatible implementation is required.");
    }
    // Keep browser-native fetch from receiving JSONTransport as its receiver.
    this.#fetch = (...arguments_) => fetchImplementation(...arguments_);
  }

  async request<T>(request: ConditionalJSONRequest): Promise<SDKConditionalResponse<T>>;
  async request<T>(request: JSONRequest): Promise<SDKResponse<T>>;
  async request<T>(
    request: JSONRequest | ConditionalJSONRequest,
  ): Promise<SDKResponse<T> | SDKConditionalResponse<T>> {
    const headers = new Headers({
      Accept: "application/json, application/problem+json",
      Authorization: `Bearer ${request.bearerToken}`,
      ...request.headers,
    });
    if (request.body !== undefined && request.rawBody !== undefined) {
      throw new TypeError("A request cannot contain both JSON and raw bodies.");
    }
    let body: BodyInit | undefined = request.rawBody;
    if (request.body !== undefined) {
      headers.set("Content-Type", "application/json");
      body = JSON.stringify(request.body);
    }

    const init: RequestInit = {
      method: request.method,
      headers,
      ...(body === undefined ? {} : { body }),
      ...(request.signal === undefined ? {} : { signal: request.signal }),
    };

    let response: Response;
    try {
      response = await this.#fetch(new URL(stripLeadingSlash(request.path), this.#baseUrl), init);
    } catch (cause) {
      const aborted = request.signal?.aborted === true || isAbortError(cause);
      throw new IdenqaTransportError(
        aborted ? "The request was aborted." : "The request could not reach Idenqa Core.",
        aborted,
        cause,
      );
    }

    const headerRequestId = optionalHeader(response.headers, "X-Request-ID");
    if (response.status === 304 && "allowNotModified" in request) {
      const etag = optionalHeader(response.headers, "ETag");
      if (headerRequestId === undefined || etag === undefined) {
        throw new IdenqaProtocolError(
          "Idenqa Core returned an incomplete not-modified response.",
          response.status,
          headerRequestId,
        );
      }
      return { notModified: true, requestId: headerRequestId, etag };
    }
    if (!response.ok) {
      throw await apiError(response, headerRequestId);
    }
    if (headerRequestId === undefined) {
      throw new IdenqaProtocolError(
        "Idenqa Core returned a successful response without X-Request-ID.",
        response.status,
      );
    }

    let data: T;
    try {
      data = (await response.json()) as T;
    } catch (cause) {
      throw new IdenqaProtocolError(
        "Idenqa Core returned an invalid JSON response.",
        response.status,
        headerRequestId,
        cause,
      );
    }

    const etag = optionalHeader(response.headers, "ETag");
    const location = optionalHeader(response.headers, "Location");
    const successful = {
      data,
      requestId: headerRequestId,
      ...(etag === undefined ? {} : { etag }),
      ...(location === undefined ? {} : { location }),
    };
    return "allowNotModified" in request
      ? { ...successful, notModified: false as const }
      : successful;
  }

  async requestCanonicalJSON<T>(
    request: ConditionalJSONRequest,
    expectedMediaType: string,
  ): Promise<SDKConditionalResponse<CanonicalJSONData<T>>>;
  async requestCanonicalJSON<T>(
    request: JSONRequest,
    expectedMediaType: string,
  ): Promise<SDKResponse<CanonicalJSONData<T>>>;
  async requestCanonicalJSON<T>(
    request: JSONRequest | ConditionalJSONRequest,
    expectedMediaType: string,
  ): Promise<SDKResponse<CanonicalJSONData<T>> | SDKConditionalResponse<CanonicalJSONData<T>>> {
    const headers = new Headers({
      Accept: `${expectedMediaType}, application/problem+json`,
      Authorization: `Bearer ${request.bearerToken}`,
      ...request.headers,
    });
    const init: RequestInit = {
      method: request.method,
      headers,
      ...(request.signal === undefined ? {} : { signal: request.signal }),
    };

    let response: Response;
    try {
      response = await this.#fetch(new URL(stripLeadingSlash(request.path), this.#baseUrl), init);
    } catch (cause) {
      const aborted = request.signal?.aborted === true || isAbortError(cause);
      throw new IdenqaTransportError(
        aborted ? "The request was aborted." : "The request could not reach Idenqa Core.",
        aborted,
        cause,
      );
    }

    const headerRequestId = optionalHeader(response.headers, "X-Request-ID");
    if (response.status === 304 && "allowNotModified" in request) {
      const etag = optionalHeader(response.headers, "ETag");
      if (headerRequestId === undefined || etag === undefined) {
        throw new IdenqaProtocolError(
          "Idenqa Core returned an incomplete not-modified response.",
          response.status,
          headerRequestId,
        );
      }
      return { notModified: true, requestId: headerRequestId, etag };
    }
    if (!response.ok) throw await apiError(response, headerRequestId);
    if (headerRequestId === undefined) {
      throw new IdenqaProtocolError(
        "Idenqa Core returned a successful response without X-Request-ID.",
        response.status,
      );
    }
    const contentType = optionalHeader(response.headers, "Content-Type")?.split(";", 1)[0]?.trim();
    if (contentType !== expectedMediaType) {
      throw new IdenqaProtocolError(
        "Idenqa Core returned an unexpected canonical JSON media type.",
        response.status,
        headerRequestId,
      );
    }

    const canonical = await response.text();
    let document: T;
    try {
      document = JSON.parse(canonical) as T;
    } catch (cause) {
      throw new IdenqaProtocolError(
        "Idenqa Core returned invalid canonical JSON.",
        response.status,
        headerRequestId,
        cause,
      );
    }
    const etag = optionalHeader(response.headers, "ETag");
    const successful = {
      data: { canonical, document },
      requestId: headerRequestId,
      ...(etag === undefined ? {} : { etag }),
    };
    return "allowNotModified" in request
      ? { ...successful, notModified: false as const }
      : successful;
  }
}

function normalizeBaseUrl(value: string | URL): URL {
  const url = new URL(value.toString());
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new TypeError("baseUrl must use http or https.");
  }
  if (url.username !== "" || url.password !== "") {
    throw new TypeError("baseUrl must not contain credentials.");
  }
  if (url.search !== "" || url.hash !== "") {
    throw new TypeError("baseUrl must not contain a query or fragment.");
  }
  url.pathname = `${url.pathname.replace(/\/+$/, "")}/`;
  return url;
}

function stripLeadingSlash(value: string): string {
  return value.replace(/^\/+/, "");
}

function optionalHeader(headers: Headers, name: string): string | undefined {
  const value = headers.get(name);
  return value === null || value === "" ? undefined : value;
}

async function apiError(response: Response, headerRequestId?: string): Promise<Error> {
  let value: unknown;
  try {
    value = await response.json();
  } catch (cause) {
    return new IdenqaProtocolError(
      "Idenqa Core returned a non-JSON error response.",
      response.status,
      headerRequestId,
      cause,
    );
  }
  if (!isWireProblem(value)) {
    return new IdenqaProtocolError(
      "Idenqa Core returned an invalid problem response.",
      response.status,
      headerRequestId,
    );
  }
  if (value.status !== response.status) {
    return new IdenqaProtocolError(
      "The problem status does not match the HTTP status.",
      response.status,
      headerRequestId,
    );
  }

  const problem: ProblemDetails = {
    type: value.type,
    title: value.title,
    status: value.status,
    code: value.code,
    detail: value.detail,
    requestId: value.request_id,
  };
  return new IdenqaAPIError(problem, optionalHeader(response.headers, "Retry-After"));
}

function isWireProblem(value: unknown): value is WireProblem {
  if (typeof value !== "object" || value === null) return false;
  const problem = value as Readonly<Record<string, unknown>>;
  return (
    typeof problem.type === "string" &&
    typeof problem.title === "string" &&
    typeof problem.status === "number" &&
    typeof problem.code === "string" &&
    typeof problem.detail === "string" &&
    typeof problem.request_id === "string"
  );
}

function isAbortError(value: unknown): boolean {
  return value instanceof Error && value.name === "AbortError";
}
