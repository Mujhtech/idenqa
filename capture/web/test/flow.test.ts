import { describe, expect, it, vi } from "vitest";

import {
  IdenqaTransportError,
  type SDKConditionalResponse,
  type CaptureObservation,
  type CaptureOutcome,
  type CaptureAuthoritySnapshot,
  type CaptureProgress,
  type CaptureRealtimeEvent,
  type EvidenceUpload,
  type ProcessingAuthority,
  type SDKResponse,
  type SubjectResponse,
  type VerificationSession,
} from "@idenqa/sdk";

import {
  CAPTURE_EXPERIENCE_VERSION,
  CaptureFlowController,
  CaptureFlowError,
  type CaptureFlowClient,
  type CaptureFlowSnapshot,
} from "../src/index.js";

const session = verificationSession();
const snapshot = authoritySnapshot();
const capabilities = {
  supportedMethods: ["idenqa.method.file_upload"],
  availableMethods: ["idenqa.method.file_upload"],
};

describe("CaptureFlowController", () => {
  it("retries a lost document-selection response with the same key and recovers the Core branch", async () => {
    const base = session.requirements.requirements[0]!;
    let current: VerificationSession = {
      ...session,
      requirements: {
        ...session.requirements,
        requirements: [
          {
            ...base,
            key: "document",
            evidence_type: "idenqa.evidence.document_image",
            artefacts: ["idenqa.artefact.document_back", "idenqa.artefact.document_front"],
            document_options: [
              { id: "passport", label: "passport", artefacts: ["idenqa.artefact.document_front"] },
              {
                id: "driver_license",
                label: "driver license",
                artefacts: ["idenqa.artefact.document_front", "idenqa.artefact.document_back"],
              },
            ],
          },
        ],
      },
    };
    const selectDocument = vi
      .fn<NonNullable<CaptureFlowClient["selectDocument"]>>()
      .mockRejectedValueOnce(new Error("response lost"))
      .mockImplementationOnce(async () => {
        current = { ...current, version: 2, documentSelections: { document: "passport" } };
        return response(current);
      });
    const getSession = () => Promise.resolve(response(current));
    const acknowledged = authoritySnapshot({ latestResponse: subjectResponse("acknowledge") });
    const flowClient = {
      ...client({
        getSession,
        snapshot: {
          ...acknowledged,
          authority: {
            ...acknowledged.authority,
            evidenceTypes: ["idenqa.evidence.document_image"],
          },
        },
      }),
      selectDocument,
    };
    const controller = new CaptureFlowController(flowClient, capabilities, {
      idempotencyKeyFactory: () => "stable_selection_key",
    });
    const before = await controller.load();
    if (!("plan" in before)) throw new Error("Expected capture plan");
    await expect(
      controller.uploadFile(
        before.plan.requirements[0]!.steps[0]!,
        new Blob(["test"], { type: "image/png" }),
      ),
    ).rejects.toThrow("Choose a permitted document");
    await expect(controller.selectDocument("document", "passport")).rejects.toThrow(
      "response lost",
    );
    const after = await controller.selectDocument("document", "passport");
    expect(selectDocument.mock.calls[0]).toEqual(selectDocument.mock.calls[1]);
    if (!("plan" in after)) throw new Error("Expected capture plan");
    expect(after.plan.requirements[0]?.steps.map((step) => step.artefact)).toEqual([
      "idenqa.artefact.document_front",
    ]);
    expect(after.session.requirements).toEqual(before.session.requirements);
    const restored = await new CaptureFlowController(flowClient, capabilities).load();
    expect("plan" in restored && restored.plan.requirements[0]?.selectedDocument).toBe("passport");
    await expect(controller.selectDocument("document", "unlisted")).rejects.toThrow(
      "not permitted",
    );
    expect(selectDocument).toHaveBeenCalledTimes(2);
  });
  it("keeps a recorded document selection when a later session read resolves stale", async () => {
    const base = session.requirements.requirements[0]!;
    const staleSession: VerificationSession = {
      ...session,
      requirements: {
        ...session.requirements,
        requirements: [
          {
            ...base,
            key: "document",
            evidence_type: "idenqa.evidence.document_image",
            artefacts: ["idenqa.artefact.document_front", "idenqa.artefact.document_back"],
            document_options: [
              { id: "passport", label: "passport", artefacts: ["idenqa.artefact.document_front"] },
              {
                id: "driver_license",
                label: "driver license",
                artefacts: [
                  "idenqa.artefact.document_front",
                  "idenqa.artefact.document_back",
                ],
              },
            ],
          },
        ],
      },
    };
    const selectDocument = vi
      .fn<NonNullable<CaptureFlowClient["selectDocument"]>>()
      .mockResolvedValue(
        response({ ...staleSession, version: 2, documentSelections: { document: "passport" } }),
      );
    const acknowledged = authoritySnapshot({ latestResponse: subjectResponse("acknowledge") });
    const controller = new CaptureFlowController(
      {
        ...client({
          // The read that raced the command still returns the pre-command view.
          getSession: () => Promise.resolve(response(staleSession)),
          snapshot: {
            ...acknowledged,
            authority: {
              ...acknowledged.authority,
              evidenceTypes: ["idenqa.evidence.document_image"],
            },
          },
        }),
        selectDocument,
      },
      capabilities,
      { idempotencyKeyFactory: () => "stable_selection_key" },
    );
    const before = await controller.load();
    if (!("plan" in before)) throw new Error("Expected capture plan");
    const selected = await controller.selectDocument("document", "passport");
    if (!("plan" in selected)) throw new Error("Expected capture plan");
    expect(selected.plan.requirements[0]?.selectedDocument).toBe("passport");
    expect(selected.session.version).toBe(2);

    const refreshed = await controller.refresh();
    if (!("plan" in refreshed)) throw new Error("Expected capture plan");
    expect(refreshed.plan.requirements[0]?.selectedDocument).toBe("passport");
    expect(refreshed.session.version).toBe(2);
  });
  it("loads a terminal authoritative outcome without calling active-capture endpoints", async () => {
    const getSession = vi.fn<CaptureFlowClient["getSession"]>();
    const controller = new CaptureFlowController(
      client({
        getSession,
        getOutcome: () =>
          Promise.resolve(
            response({
              verificationId: session.id,
              state: "verified",
              sessionVersion: 4,
              updatedAt: "2026-08-30T00:00:04Z",
            } satisfies CaptureOutcome),
          ),
      }),
      capabilities,
    );

    await expect(controller.load()).resolves.toEqual({
      status: "verified",
      outcome: {
        verificationId: session.id,
        state: "verified",
        sessionVersion: 4,
        updatedAt: "2026-08-30T00:00:04Z",
      },
    });
    expect(getSession).not.toHaveBeenCalled();
  });

  it("recovers when capture stops between outcome and capture-only reads", async () => {
    const getOutcome = vi
      .fn<CaptureFlowClient["getOutcome"]>()
      .mockResolvedValueOnce(
        response({
          verificationId: session.id,
          state: "capture_required",
          sessionVersion: 1,
          updatedAt: session.updatedAt,
        }),
      )
      .mockResolvedValueOnce(
        response({
          verificationId: session.id,
          state: "not_verified",
          sessionVersion: 4,
          updatedAt: "2026-08-30T00:00:04Z",
        }),
      );
    const getSession = vi
      .fn<CaptureFlowClient["getSession"]>()
      .mockRejectedValue(new Error("capture no longer accepted"));
    const controller = new CaptureFlowController(client({ getOutcome, getSession }), capabilities);

    await expect(controller.load()).resolves.toEqual({
      status: "not_verified",
      outcome: {
        verificationId: session.id,
        state: "not_verified",
        sessionVersion: 4,
        updatedAt: "2026-08-30T00:00:04Z",
      },
    });
    expect(getOutcome).toHaveBeenCalledTimes(2);
  });

  it("polls from accepted capture through processing to a terminal outcome", async () => {
    const getOutcome = vi
      .fn<CaptureFlowClient["getOutcome"]>()
      .mockResolvedValueOnce(
        response({
          verificationId: session.id,
          state: "capture_required",
          sessionVersion: 1,
          updatedAt: session.updatedAt,
        }),
      )
      .mockResolvedValueOnce(
        response({
          verificationId: session.id,
          state: "processing",
          sessionVersion: 2,
          updatedAt: "2026-08-30T00:00:02Z",
        }),
      )
      .mockResolvedValueOnce(
        response({
          verificationId: session.id,
          state: "verified",
          sessionVersion: 3,
          updatedAt: "2026-08-30T00:00:03Z",
        }),
      );
    const controller = new CaptureFlowController(client({ getOutcome }), capabilities, {
      recoveryPollingIntervalMs: 250,
    });
    await controller.load();
    const snapshots: CaptureFlowSnapshot[] = [];

    await controller.pollOutcome((next) => snapshots.push(next));

    expect(snapshots.map((next) => next.status)).toEqual(["processing", "verified"]);
  });

  it("requires an initial load before an authoritative refresh", async () => {
    const controller = new CaptureFlowController(client(), capabilities);

    await expect(controller.refresh()).rejects.toMatchObject({
      code: "CAPTURE_FLOW_INVALID_STATE",
    } satisfies Partial<CaptureFlowError>);
    await controller.load();
    await expect(controller.refresh()).resolves.toMatchObject({
      status: "notice_required",
      session,
    });
  });

  it("loads the correlated exact notice before making capture available", async () => {
    const controller = new CaptureFlowController(client(), capabilities);

    await expect(controller.load()).resolves.toMatchObject({
      status: "notice_required",
      session,
      authoritySnapshot: snapshot,
    });
  });

  it("recovers the authoritative REST snapshot after realtime progress", async () => {
    const getProgress = vi
      .fn<CaptureFlowClient["getProgress"]>()
      .mockResolvedValueOnce(response({ verificationId: session.id, completions: [] }))
      .mockResolvedValueOnce(response({ verificationId: session.id, completions: [] }));
    const realtimeEvent = {
      type: "capture.progress",
      messageId: "msg_01M11HEQG00000000000000000",
      verificationId: session.id,
      connectionId: "con_01M11HEQG00000000000000000",
      sequence: 2,
      occurredAt: "2026-08-30T12:00:00Z",
      payload: { completedSteps: 0, totalSteps: 1 },
    } satisfies CaptureRealtimeEvent;
    const close = vi.fn();
    const controller = new CaptureFlowController(
      client({
        getProgress,
        observe: () => ({
          async *[Symbol.asyncIterator]() {
            yield realtimeEvent;
          },
          reportStep: vi.fn(),
          close,
        }),
      }),
      capabilities,
    );
    await controller.load();
    const events: CaptureRealtimeEvent[] = [];
    const snapshots: CaptureFlowSnapshot[] = [];

    await controller.observe(
      (event) => events.push(event),
      (next) => snapshots.push(next),
    );

    expect(events).toEqual([realtimeEvent]);
    expect(snapshots).toHaveLength(1);
    expect(getProgress).toHaveBeenCalledTimes(2);
    expect(close).toHaveBeenCalledOnce();
  });

  it("switches from active capture to a Core-authored outcome after lifecycle progress", async () => {
    const getOutcome = vi
      .fn<CaptureFlowClient["getOutcome"]>()
      .mockResolvedValueOnce(
        response({
          verificationId: session.id,
          state: "capture_required",
          sessionVersion: 1,
          updatedAt: session.updatedAt,
        }),
      )
      .mockResolvedValueOnce(
        response({
          verificationId: session.id,
          state: "verified",
          sessionVersion: 4,
          updatedAt: "2026-08-30T00:00:04Z",
        }),
      );
    const stateChanged = {
      type: "session.state_changed",
      messageId: "msg_01M11HEQG00000000000000000",
      verificationId: session.id,
      connectionId: "con_01M11HEQG00000000000000000",
      sequence: 2,
      occurredAt: "2026-08-30T00:00:04Z",
      payload: { state: "completed", sessionVersion: 4 },
    } satisfies CaptureRealtimeEvent;
    const controller = new CaptureFlowController(
      client({
        getOutcome,
        observe: () => ({
          async *[Symbol.asyncIterator]() {
            yield stateChanged;
          },
          reportStep: vi.fn(),
          close: vi.fn(),
        }),
      }),
      capabilities,
    );
    await controller.load();
    const snapshots: CaptureFlowSnapshot[] = [];

    await controller.observe(
      () => undefined,
      (next) => snapshots.push(next),
    );

    expect(snapshots).toEqual([
      {
        status: "verified",
        outcome: {
          verificationId: session.id,
          state: "verified",
          sessionVersion: 4,
          updatedAt: "2026-08-30T00:00:04Z",
        },
      },
    ]);
  });

  it("recovers the authoritative snapshot after verification check progress", async () => {
    const getProgress = vi
      .fn<CaptureFlowClient["getProgress"]>()
      .mockResolvedValue(response({ verificationId: session.id, completions: [] }));
    const realtimeEvent = {
      type: "verification.check.progress",
      messageId: "msg_01M11HEQG00000000000000000",
      verificationId: session.id,
      connectionId: "con_01M11HEQG00000000000000000",
      sequence: 2,
      eventCursor: 1,
      occurredAt: "2026-08-30T12:00:00Z",
      payload: {
        checkId: "chk_01M11HEQG00000000000000000",
        state: "awaiting_provider",
        checkVersion: 2,
      },
    } satisfies CaptureRealtimeEvent;
    const controller = new CaptureFlowController(
      client({
        getProgress,
        observe: () => ({
          async *[Symbol.asyncIterator]() {
            yield realtimeEvent;
          },
          reportStep: vi.fn(),
          close: vi.fn(),
        }),
      }),
      capabilities,
    );
    await controller.load();
    const events: CaptureRealtimeEvent[] = [];
    const snapshots: CaptureFlowSnapshot[] = [];
    await controller.observe(
      (event) => events.push(event),
      (next) => snapshots.push(next),
    );
    expect(events).toEqual([realtimeEvent]);
    expect(snapshots).toHaveLength(1);
    expect(getProgress).toHaveBeenCalledTimes(2);
  });

  it("falls back to conditional REST polling after realtime transport exhaustion", async () => {
    const etag = '"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"';
    const getProgress = vi
      .fn<CaptureFlowClient["getProgress"]>()
      .mockResolvedValue(response({ verificationId: session.id, completions: [] }, etag));
    const changed = {
      notModified: false,
      ...response({ verificationId: session.id, completions: [] }, etag),
    } satisfies SDKConditionalResponse<CaptureProgress>;
    const pollProgress = vi
      .fn<NonNullable<CaptureFlowClient["pollProgress"]>>()
      .mockResolvedValue(changed);
    const failedObservation: CaptureObservation = {
      async *[Symbol.asyncIterator]() {
        throw new IdenqaTransportError("reconnect exhausted", false);
      },
      reportStep: vi.fn(),
      close: vi.fn(),
    };
    const controller = new CaptureFlowController(
      client({ getProgress, pollProgress, observe: () => failedObservation }),
      capabilities,
      { recoveryPollingIntervalMs: 250 },
    );
    await controller.load();
    const abort = new AbortController();

    await controller.observe(
      () => undefined,
      () => abort.abort(),
      abort.signal,
    );

    expect(pollProgress).toHaveBeenCalledWith(etag, { signal: abort.signal });
    expect(getProgress).toHaveBeenCalledTimes(2);
  });

  it("records consent with the exact notice locale, experience version, and one retry key", async () => {
    const respond = vi
      .fn<CaptureFlowClient["respond"]>()
      .mockRejectedValueOnce(new Error("temporary failure"))
      .mockResolvedValue(response(subjectResponse("consent")));
    const controller = new CaptureFlowController(
      client({
        snapshot: authoritySnapshot({ consentRequired: true }),
        respond,
      }),
      capabilities,
      { idempotencyKeyFactory: () => "capture-notice-1" },
    );
    await controller.load();

    await expect(controller.respond("consent")).rejects.toThrow("temporary failure");
    await expect(controller.respond("consent")).resolves.toMatchObject({
      status: "capture_ready",
    });
    expect(respond).toHaveBeenCalledTimes(2);
    expect(respond).toHaveBeenNthCalledWith(
      2,
      {
        action: "consent",
        locale: "en",
        renderedExperienceVersion: CAPTURE_EXPERIENCE_VERSION,
      },
      { idempotencyKey: "capture-notice-1" },
    );
  });

  it("uses acknowledgement when consent is not required", async () => {
    const controller = new CaptureFlowController(client(), capabilities, {
      idempotencyKeyFactory: () => "capture-notice-2",
    });
    await controller.load();

    await expect(controller.respond("acknowledge")).resolves.toMatchObject({
      status: "capture_ready",
    });
  });

  it("keeps refusal from exposing capture methods", async () => {
    const controller = new CaptureFlowController(client(), capabilities);
    await controller.load();

    await expect(controller.respond("refuse")).resolves.toMatchObject({ status: "refused" });
    await expect(controller.respond("acknowledge")).rejects.toMatchObject({
      code: "CAPTURE_FLOW_INVALID_STATE",
    } satisfies Partial<CaptureFlowError>);
  });

  it("blocks a non-active authority even when a prior response exists", async () => {
    const controller = new CaptureFlowController(
      client({
        snapshot: authoritySnapshot({
          state: "withdrawn",
          latestResponse: subjectResponse("acknowledge"),
        }),
      }),
      capabilities,
    );

    await expect(controller.load()).resolves.toMatchObject({ status: "authority_blocked" });
  });

  it("fails closed when the authority and session identifiers do not correlate", async () => {
    const controller = new CaptureFlowController(
      client({
        snapshot: authoritySnapshot({
          verificationId: "ver_01M11HEQG00000000000000099",
        }),
      }),
      capabilities,
    );

    await expect(controller.load()).rejects.toMatchObject({
      code: "CAPTURE_FLOW_INVALID_SNAPSHOT",
    } satisfies Partial<CaptureFlowError>);
  });

  it("fails closed when authority scope does not cover the session requirements", async () => {
    const mismatched = authoritySnapshot();
    const controller = new CaptureFlowController(
      client({
        snapshot: {
          ...mismatched,
          authority: { ...mismatched.authority, evidenceTypes: [] },
        },
      }),
      capabilities,
    );

    await expect(controller.load()).rejects.toMatchObject({
      code: "CAPTURE_FLOW_INVALID_SNAPSHOT",
    } satisfies Partial<CaptureFlowError>);
  });

  it("recovers accepted completion without issuing another upload", async () => {
    const progress: CaptureProgress = {
      verificationId: session.id,
      completions: [
        {
          uploadId: "upl_01M11HEQG00000000000000000",
          evidenceId: "evd_01M11HEQG00000000000000000",
          requirementKey: "selfie",
          evidenceType: "idenqa.evidence.selfie_image",
          artefact: "idenqa.artefact.selfie_image",
          acquisitionMethod: "idenqa.method.file_upload",
        },
      ],
    };
    const createEvidenceUpload = vi.fn<CaptureFlowClient["createEvidenceUpload"]>();
    const controller = new CaptureFlowController(
      client({ progress, createEvidenceUpload }),
      capabilities,
    );

    await expect(controller.load()).resolves.toMatchObject({ progress });
    expect(createEvidenceUpload).not.toHaveBeenCalled();
  });

  it("recovers the current ETag before retrying an ambiguous whole-body upload", async () => {
    const createEvidenceUpload = vi
      .fn<CaptureFlowClient["createEvidenceUpload"]>()
      .mockResolvedValue(response(upload("issued", 1), '"1"'));
    const getEvidenceUpload = vi
      .fn<CaptureFlowClient["getEvidenceUpload"]>()
      .mockResolvedValue(response(upload("issued", 3), '"3"'));
    const uploadEvidence = vi
      .fn<CaptureFlowClient["uploadEvidence"]>()
      .mockRejectedValueOnce(new Error("response lost"))
      .mockResolvedValue(response(upload("accepted", 5), '"5"'));
    const controller = new CaptureFlowController(
      client({ createEvidenceUpload, getEvidenceUpload, uploadEvidence }),
      capabilities,
      {
        idempotencyKeyFactory: () => "notice-1",
        uploadIdempotencyKeyFactory: () => "upload-1",
      },
    );
    await controller.load();
    const ready = await controller.respond("acknowledge");
    const body = new Blob([new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])], {
      type: "image/png",
    });
    const step = ready.plan.requirements[0]!.steps[0]!;

    await expect(controller.uploadFile(step, body)).rejects.toThrow("response lost");
    await expect(controller.uploadFile(step, body)).resolves.toMatchObject({ state: "accepted" });

    expect(createEvidenceUpload).toHaveBeenCalledTimes(1);
    expect(createEvidenceUpload).toHaveBeenCalledWith(
      expect.objectContaining({
        requirementKey: "selfie",
        expectedBytes: 8,
        mediaType: "image/png",
        region: "idenqa.region.synthetic",
      }),
      { idempotencyKey: "upload-1" },
    );
    expect(getEvidenceUpload).toHaveBeenCalledTimes(1);
    expect(uploadEvidence).toHaveBeenNthCalledWith(
      2,
      "upl_01M11HEQG00000000000000000",
      body,
      expect.objectContaining({ etag: '"3"' }),
    );
  });

  it("reuses the initiation key when the intent response is ambiguous", async () => {
    const createEvidenceUpload = vi
      .fn<CaptureFlowClient["createEvidenceUpload"]>()
      .mockRejectedValueOnce(new Error("response lost"))
      .mockResolvedValue(response(upload("issued", 1), '"1"'));
    const controller = new CaptureFlowController(
      client({
        createEvidenceUpload,
        uploadEvidence: vi
          .fn<CaptureFlowClient["uploadEvidence"]>()
          .mockResolvedValue(response(upload("accepted", 3), '"3"')),
      }),
      capabilities,
      {
        idempotencyKeyFactory: () => "notice-1",
        uploadIdempotencyKeyFactory: () => "upload-stable-1",
      },
    );
    await controller.load();
    const ready = await controller.respond("acknowledge");
    const body = new Blob([new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])], {
      type: "image/png",
    });
    const step = ready.plan.requirements[0]!.steps[0]!;

    await expect(controller.uploadFile(step, body)).rejects.toThrow("response lost");
    await expect(controller.uploadFile(step, body)).resolves.toMatchObject({ state: "accepted" });

    expect(createEvidenceUpload).toHaveBeenCalledTimes(2);
    expect(createEvidenceUpload.mock.calls.map((call) => call[1].idempotencyKey)).toEqual([
      "upload-stable-1",
      "upload-stable-1",
    ]);
  });

  it("uploads a camera frame with the live-camera acquisition binding", async () => {
    const cameraSession = cameraVerificationSession();
    const createEvidenceUpload = vi
      .fn<CaptureFlowClient["createEvidenceUpload"]>()
      .mockResolvedValue(response(upload("issued", 1, "idenqa.method.live_camera"), '"1"'));
    const controller = new CaptureFlowController(
      client({
        session: cameraSession,
        createEvidenceUpload,
        uploadEvidence: vi
          .fn<CaptureFlowClient["uploadEvidence"]>()
          .mockResolvedValue(response(upload("accepted", 3, "idenqa.method.live_camera"), '"3"')),
      }),
      {
        supportedMethods: ["idenqa.method.live_camera", "idenqa.method.file_upload"],
        availableMethods: ["idenqa.method.live_camera", "idenqa.method.file_upload"],
      },
    );
    await controller.load();
    const ready = await controller.respond("acknowledge");
    const body = new Blob([new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])], {
      type: "image/png",
    });

    await expect(
      controller.uploadCamera(ready.plan.requirements[0]!.steps[0]!, body),
    ).resolves.toMatchObject({ state: "accepted", acquisitionMethod: "idenqa.method.live_camera" });
    expect(createEvidenceUpload).toHaveBeenCalledWith(
      expect.objectContaining({ acquisitionMethod: "idenqa.method.live_camera" }),
      expect.any(Object),
    );
  });

  it("replans to an explicitly approved capture_failed fallback", async () => {
    const cameraSession = cameraVerificationSession(true);
    const controller = new CaptureFlowController(client({ session: cameraSession }), {
      supportedMethods: ["idenqa.method.live_camera", "idenqa.method.file_upload"],
      availableMethods: ["idenqa.method.live_camera", "idenqa.method.file_upload"],
    });
    await controller.load();
    const ready = await controller.respond("acknowledge");

    expect(
      controller.captureFailed(ready.plan.requirements[0]!.steps[0]!).plan.requirements[0],
    ).toMatchObject({
      steps: [
        {
          methodOptions: ["idenqa.method.file_upload"],
          fallbackCondition: "capture_failed",
        },
      ],
    });
  });
});

function client(
  overrides: {
    readonly snapshot?: CaptureAuthoritySnapshot;
    readonly session?: VerificationSession;
    readonly progress?: CaptureProgress;
    readonly getOutcome?: CaptureFlowClient["getOutcome"];
    readonly getSession?: CaptureFlowClient["getSession"];
    readonly getAuthority?: CaptureFlowClient["getAuthority"];
    readonly getProgress?: CaptureFlowClient["getProgress"];
    readonly pollProgress?: CaptureFlowClient["pollProgress"];
    readonly observe?: CaptureFlowClient["observe"];
    readonly respond?: CaptureFlowClient["respond"];
    readonly createEvidenceUpload?: CaptureFlowClient["createEvidenceUpload"];
    readonly getEvidenceUpload?: CaptureFlowClient["getEvidenceUpload"];
    readonly uploadEvidence?: CaptureFlowClient["uploadEvidence"];
  } = {},
): CaptureFlowClient {
  const authority = overrides.snapshot ?? snapshot;
  return {
    getOutcome:
      overrides.getOutcome ??
      (() =>
        Promise.resolve(
          response({
            verificationId: (overrides.session ?? session).id,
            state: "capture_required",
            sessionVersion: (overrides.session ?? session).version,
            updatedAt: (overrides.session ?? session).updatedAt,
          }),
        )),
    getSession:
      overrides.getSession ?? (() => Promise.resolve(response(overrides.session ?? session))),
    getAuthority: overrides.getAuthority ?? (() => Promise.resolve(response(authority))),
    getProgress:
      overrides.getProgress ??
      (() =>
        Promise.resolve(
          response(
            overrides.progress ?? {
              verificationId: (overrides.session ?? session).id,
              completions: [],
            },
          ),
        )),
    ...(overrides.observe === undefined ? {} : { observe: overrides.observe }),
    ...(overrides.pollProgress === undefined ? {} : { pollProgress: overrides.pollProgress }),
    respond:
      overrides.respond ?? ((input) => Promise.resolve(response(subjectResponse(input.action)))),
    createEvidenceUpload:
      overrides.createEvidenceUpload ?? vi.fn<CaptureFlowClient["createEvidenceUpload"]>(),
    getEvidenceUpload:
      overrides.getEvidenceUpload ?? vi.fn<CaptureFlowClient["getEvidenceUpload"]>(),
    uploadEvidence: overrides.uploadEvidence ?? vi.fn<CaptureFlowClient["uploadEvidence"]>(),
  };
}

function response<T>(data: T, etag?: string): SDKResponse<T> {
  return {
    data,
    requestId: "req_01M11HEQG00000000000000000",
    ...(etag === undefined ? {} : { etag }),
  };
}

function upload(
  state: EvidenceUpload["state"],
  version: number,
  acquisitionMethod = "idenqa.method.file_upload",
): EvidenceUpload {
  return {
    id: "upl_01M11HEQG00000000000000000",
    evidenceId: "evd_01M11HEQG00000000000000000",
    state,
    version,
    attempt: state === "issued" && version === 1 ? 0 : 1,
    requirementKey: "selfie",
    evidenceType: "idenqa.evidence.selfie_image",
    artefact: "idenqa.artefact.selfie_image",
    acquisitionMethod,
    assurances: [],
    allowedMediaTypes: ["image/png"],
    maximumBytes: 16 * 1024 * 1024,
    expectedBytes: 8,
    mediaType: "image/png",
    region: "idenqa.region.synthetic",
    createdAt: "2026-08-30T00:00:00Z",
    updatedAt: "2026-08-30T00:00:01Z",
    expiresAt: "2026-08-30T00:15:00Z",
    ...(state === "accepted" ? { acceptedAt: "2026-08-30T00:00:01Z" } : {}),
  };
}

function cameraVerificationSession(withFallback = false): VerificationSession {
  const base = verificationSession();
  const requirement = base.requirements.requirements[0]!;
  return {
    ...base,
    requirements: {
      ...base.requirements,
      requirements: [
        {
          ...requirement,
          acquisition: { strategy: "any_of", methods: ["idenqa.method.live_camera"] },
          fallbacks: withFallback
            ? [
                {
                  on: ["capture_failed"],
                  acquisition: { strategy: "any_of", methods: ["idenqa.method.file_upload"] },
                },
              ]
            : [],
        },
      ],
    },
  };
}

function verificationSession(): VerificationSession {
  return {
    id: "ver_01M11HEQG00000000000000000",
    state: "collecting",
    version: 1,
    profileId: "prf_01M11HEQG00000000000000000",
    profileRevision: 1,
    profileDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    policyId: "pol_01M11HEQG00000000000000000",
    region: "global",
    requirements: {
      schema_version: 1,
      registry: {
        schema_version: 1,
        revision: 1,
        digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
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
    },
    createdAt: "2026-08-30T00:00:00Z",
    updatedAt: "2026-08-30T00:00:00Z",
    expiresAt: "2026-08-30T01:00:00Z",
  };
}

function authoritySnapshot(
  overrides: {
    readonly consentRequired?: boolean;
    readonly state?: ProcessingAuthority["state"];
    readonly verificationId?: string;
    readonly latestResponse?: SubjectResponse;
  } = {},
): CaptureAuthoritySnapshot {
  return {
    authority: {
      id: "aut_01M11HEQG00000000000000000",
      subjectId: "sub_01M11HEQG00000000000000000",
      verificationId: overrides.verificationId ?? session.id,
      noticeId: "ntc_01M11HEQG00000000000000000",
      category: "tenant.authority.customer_declared",
      purpose: "idenqa.purpose.identity_verification",
      jurisdiction: "tenant.jurisdiction.synthetic",
      policyPack: "tenant.policy.synthetic_v1",
      consentRequired: overrides.consentRequired ?? false,
      requirementPurposes: ["idenqa.purpose.identity_verification"],
      evidenceTypes: ["idenqa.evidence.selfie_image"],
      recipientReference: "tenant.recipient.primary",
      recipientDisplayName: "Example Recipient",
      regions: ["idenqa.region.synthetic"],
      retentionReference: "tenant.retention.synthetic_v1",
      state: overrides.state ?? "active",
      version: 1,
      validFrom: "2026-08-30T00:00:00Z",
      expiresAt: "2026-08-30T01:00:00Z",
      createdAt: "2026-08-30T00:00:00Z",
      updatedAt: "2026-08-30T00:00:00Z",
    },
    notice: {
      id: "ntc_01M11HEQG00000000000000000",
      key: "tenant.notice.identity_verification",
      locale: "en",
      controller: "Example Controller",
      recipient: "Example Recipient",
      copy: {
        title: "Identity Verification Notice",
        summary: "We need to verify your identity.",
        purpose: "Your evidence is used only for identity verification.",
        consequences: "You may refuse and collection will not continue.",
      },
      effectiveAt: "2026-08-30T00:00:00Z",
      createdAt: "2026-08-30T00:00:00Z",
      digest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
    },
    ...(overrides.latestResponse === undefined ? {} : { latestResponse: overrides.latestResponse }),
  };
}

function subjectResponse(action: SubjectResponse["action"]): SubjectResponse {
  return {
    id: "ack_01M11HEQG00000000000000000",
    authorityId: "aut_01M11HEQG00000000000000000",
    noticeId: "ntc_01M11HEQG00000000000000000",
    subjectId: "sub_01M11HEQG00000000000000000",
    verificationId: session.id,
    action,
    locale: "en",
    renderedExperienceVersion: CAPTURE_EXPERIENCE_VERSION,
    recordedAt: "2026-08-30T00:00:01Z",
  };
}
