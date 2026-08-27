export type KnownProblemCode =
  | "INVALID_REQUEST"
  | "UNAUTHENTICATED"
  | "INSUFFICIENT_SCOPE"
  | "PROCESSING_AUTHORITY_REQUIRED"
  | "CAPTURE_ORIGIN_NOT_ALLOWED"
  | "NOT_FOUND"
  | "METHOD_NOT_ALLOWED"
  | "CONFLICT"
  | "IDEMPOTENCY_CONFLICT"
  | "PRECONDITION_FAILED"
  | "REQUEST_TOO_LARGE"
  | "PRECONDITION_REQUIRED"
  | "RATE_LIMITED"
  | "REQUEST_CANCELLED"
  | "INTERNAL_ERROR"
  | "SERVICE_UNAVAILABLE"
  | "REQUEST_TIMEOUT";

export type ProblemCode = KnownProblemCode | (string & {});

export interface ProblemDetails {
  readonly type: string;
  readonly title: string;
  readonly status: number;
  readonly code: ProblemCode;
  readonly detail: string;
  readonly requestId: string;
}

interface ErrorContext {
  readonly code: string;
  readonly requestId?: string;
  readonly status?: number;
  readonly retryAfter?: string;
  readonly cause?: unknown;
}

/** Base class for stable SDK, transport, protocol, and API failures. */
export class IdenqaError extends Error {
  readonly code: string;
  readonly requestId: string | undefined;
  readonly status: number | undefined;
  readonly retryAfter: string | undefined;

  constructor(message: string, context: ErrorContext) {
    super(message, { cause: context.cause });
    this.name = "IdenqaError";
    this.code = context.code;
    this.requestId = context.requestId;
    this.status = context.status;
    this.retryAfter = context.retryAfter;
  }
}

/** A valid RFC 9457 problem response returned by Idenqa Core. */
export class IdenqaAPIError extends IdenqaError {
  readonly problem: ProblemDetails;

  constructor(problem: ProblemDetails, retryAfter?: string) {
    super(problem.detail, {
      code: problem.code,
      requestId: problem.requestId,
      status: problem.status,
      ...(retryAfter === undefined ? {} : { retryAfter }),
    });
    this.name = "IdenqaAPIError";
    this.problem = problem;
  }
}

/** The server response did not satisfy the published HTTP contract. */
export class IdenqaProtocolError extends IdenqaError {
  constructor(message: string, status?: number, requestId?: string, cause?: unknown) {
    super(message, {
      code: "SDK_INVALID_RESPONSE",
      ...(status === undefined ? {} : { status }),
      ...(requestId === undefined ? {} : { requestId }),
      ...(cause === undefined ? {} : { cause }),
    });
    this.name = "IdenqaProtocolError";
  }
}

/** Fetch failed before a conforming HTTP response was received. */
export class IdenqaTransportError extends IdenqaError {
  readonly aborted: boolean;

  constructor(message: string, aborted: boolean, cause?: unknown) {
    super(message, {
      code: aborted ? "SDK_REQUEST_ABORTED" : "SDK_TRANSPORT_ERROR",
      ...(cause === undefined ? {} : { cause }),
    });
    this.name = "IdenqaTransportError";
    this.aborted = aborted;
  }
}
