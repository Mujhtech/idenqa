export const CAPTURE_MESSAGE_KEYS = [
  "secureCapture",
  "identityVerification",
  "preparingSecureCapture",
  "capturePreparationFailed",
  "captureCancelled",
  "preparingCapture",
  "whyInformationNeeded",
  "ifYouRefuse",
  "controller",
  "recipient",
  "consentRecorded",
  "acknowledgementRecorded",
  "refusalRecorded",
  "authorityBlocked",
  "reviewConsent",
  "reviewAcknowledgement",
  "noticeActionsLabel",
  "recordingResponse",
  "agreeAndContinue",
  "acknowledgeAndContinue",
  "refuse",
  "responseFailed",
  "chooseApprovedMethod",
  "progressSummary",
  "progressLabel",
  "captureComplete",
  "requirementProgress",
  "captureMethodsLabel",
  "capturedWith",
  "preparingFileUpload",
  "fileAccepted",
  "photoAccepted",
  "methodSelected",
  "retryUpload",
  "uploadingFile",
  "finishCameraFirst",
  "chooseFile",
  "fileGuidance",
  "maximumBytes",
  "serverSizeLimit",
  "fileUploadInProgress",
  "retryCamera",
  "startCamera",
  "requestingCamera",
  "liveCameraPreviewLabel",
  "cameraGuidance",
  "capturePhoto",
  "cancelCamera",
  "capturedPreviewLabel",
  "uploadingPhoto",
  "usePhoto",
  "retakePhoto",
  "waitingForCameraPermission",
  "cameraReady",
  "startingCameraPreview",
  "reviewCapturedPhoto",
  "uploadingCapturedPhoto",
  "cameraStepComplete",
  "fileStepComplete",
  "capabilityFallback",
  "captureFailureFallback",
  "methodFallback",
  "cameraPermissionDenied",
  "cameraFrameFailed",
  "cameraUnavailable",
  "uploadFileAction",
  "useCameraAction",
  "useMethodAction",
  "fileUploadMethod",
  "cameraMethod",
  "fileRejected",
  "photoRejected",
] as const;

export type CaptureMessageKey = (typeof CAPTURE_MESSAGE_KEYS)[number];
export type CaptureMessageValues = Readonly<Record<string, string | number>>;
export type CaptureMessageCatalogue = Readonly<
  Record<string, Partial<Readonly<Record<CaptureMessageKey, string>>>>
>;

export interface CaptureLocalizer {
  readonly locale: string;
  readonly direction: "ltr" | "rtl";
  formatNumber(value: number): string;
  text(key: CaptureMessageKey, values?: CaptureMessageValues): string;
}

export class CaptureLocalisationError extends Error {
  readonly code = "CAPTURE_LOCALISATION_INVALID";

  constructor(message: string) {
    super(message);
    this.name = "CaptureLocalisationError";
  }
}

const englishMessages: Readonly<Record<CaptureMessageKey, string>> = {
  secureCapture: "Secure Capture",
  identityVerification: "Identity Verification",
  preparingSecureCapture: "Preparing secure capture…",
  capturePreparationFailed: "Capture could not be prepared. Check the capture link and try again.",
  captureCancelled: "Capture cancelled. No evidence was collected.",
  preparingCapture: "Preparing capture…",
  whyInformationNeeded: "Why Your Information Is Needed",
  ifYouRefuse: "If You Refuse",
  controller: "Controller",
  recipient: "Recipient",
  consentRecorded: "Consent recorded. You can continue with capture.",
  acknowledgementRecorded: "Acknowledgement recorded. You can continue with capture.",
  refusalRecorded: "You chose not to continue. No evidence will be collected in this experience.",
  authorityBlocked:
    "Capture is unavailable because the processing authority is no longer active. Contact the organisation that requested this verification.",
  reviewConsent: "Review this notice, then explicitly agree to continue.",
  reviewAcknowledgement: "Review this notice, then acknowledge it to continue.",
  noticeActionsLabel: "Respond to the identity notice",
  recordingResponse: "Recording Response…",
  agreeAndContinue: "Agree & Continue",
  acknowledgeAndContinue: "Acknowledge & Continue",
  refuse: "Refuse",
  responseFailed: "Your response was not recorded. Check your connection and try again.",
  chooseApprovedMethod: "Choose an approved capture method for each required item.",
  progressSummary: "{completed} of {total} required capture steps complete.",
  progressLabel: "Evidence capture progress",
  captureComplete: "Required evidence capture is complete. Verification may continue.",
  requirementProgress: "{completed} of {total} capture steps complete.",
  captureMethodsLabel: "Capture methods for {artefact}",
  capturedWith: "Captured with {method}.",
  preparingFileUpload: "Preparing and securely uploading your file…",
  fileAccepted: "File accepted.",
  photoAccepted: "Captured photo accepted.",
  methodSelected: "{method} selected.",
  retryUpload: "Retry Upload",
  uploadingFile: "Uploading File…",
  finishCameraFirst: "Finish Camera First",
  chooseFile: "Choose File",
  fileGuidance: "{mediaTypes}.",
  maximumBytes: "Maximum {maximum}.",
  serverSizeLimit: "Your server’s configured size limit applies.",
  fileUploadInProgress: "File Upload in Progress",
  retryCamera: "Retry Camera",
  startCamera: "Start Camera",
  requestingCamera: "Requesting Camera…",
  liveCameraPreviewLabel: "Live camera preview for {artefact}",
  cameraGuidance: "Keep the required item clearly visible, then capture the photo.",
  capturePhoto: "Capture Photo",
  cancelCamera: "Cancel Camera",
  capturedPreviewLabel: "Captured {artefact} preview",
  uploadingPhoto: "Uploading Photo…",
  usePhoto: "Use Photo",
  retakePhoto: "Retake Photo",
  waitingForCameraPermission: "Waiting for camera permission…",
  cameraReady: "Camera ready.",
  startingCameraPreview: "Starting camera preview…",
  reviewCapturedPhoto: "Review the captured photo before uploading it.",
  uploadingCapturedPhoto: "Securely uploading the captured photo…",
  cameraStepComplete: "Captured photo accepted. Capture step complete.",
  fileStepComplete: "File accepted. Capture step complete.",
  capabilityFallback:
    "Your device cannot use the primary method. This approved alternative is available.",
  captureFailureFallback:
    "The camera attempt failed. This policy-approved alternative is available.",
  methodFallback:
    "The primary method is unavailable in this capture experience. This approved alternative is available.",
  cameraPermissionDenied:
    "Camera access was denied. Allow camera access in your browser settings, then retry.",
  cameraFrameFailed: "{message} Keep the camera steady, then retry.",
  cameraUnavailable:
    "The camera is unavailable. Check that it is connected and not being used by another app, then retry.",
  uploadFileAction: "Upload File",
  useCameraAction: "Use Camera",
  useMethodAction: "Use {method}",
  fileUploadMethod: "File upload",
  cameraMethod: "Camera",
  fileRejected: "The file was not accepted. Check your connection and try again.",
  photoRejected:
    "The captured photo was not accepted. Check your connection, then retry the upload.",
};

const knownMessageKeys = new Set<string>(CAPTURE_MESSAGE_KEYS);

export function createCaptureLocalizer(
  requestedLocale: string,
  catalogue: CaptureMessageCatalogue = {},
): CaptureLocalizer {
  const locale = canonicalLocale(requestedLocale);
  const catalogues = normalizeCatalogue(catalogue);
  const language = new Intl.Locale(locale).language;
  const messages = {
    ...englishMessages,
    ...(catalogues.get(language) ?? {}),
    ...(catalogues.get(locale) ?? {}),
  };
  const direction = localeDirection(locale);
  const numberFormat = new Intl.NumberFormat(locale);
  return {
    locale,
    direction,
    formatNumber: (value) => numberFormat.format(value),
    text: (key, values = {}) => interpolate(messages[key], values),
  };
}

function localeDirection(locale: string): "ltr" | "rtl" {
  const script = new Intl.Locale(locale).maximize().script;
  return script !== undefined && rtlScripts.has(script) ? "rtl" : "ltr";
}

const rtlScripts = new Set([
  "Adlm",
  "Arab",
  "Hebr",
  "Mand",
  "Nkoo",
  "Rohg",
  "Samr",
  "Syrc",
  "Thaa",
]);

function canonicalLocale(locale: string): string {
  try {
    const [canonical] = Intl.getCanonicalLocales(locale);
    if (canonical === undefined) throw new RangeError("locale is empty");
    return canonical;
  } catch {
    throw new CaptureLocalisationError(`The capture locale ${JSON.stringify(locale)} is invalid.`);
  }
}

function normalizeCatalogue(
  catalogue: CaptureMessageCatalogue,
): ReadonlyMap<string, Partial<Record<CaptureMessageKey, string>>> {
  const normalized = new Map<string, Partial<Record<CaptureMessageKey, string>>>();
  for (const [rawLocale, entries] of Object.entries(catalogue)) {
    const locale = canonicalLocale(rawLocale);
    if (normalized.has(locale)) {
      throw new CaptureLocalisationError(`The capture catalogue repeats locale ${locale}.`);
    }
    const messages: Partial<Record<CaptureMessageKey, string>> = {};
    for (const [rawKey, value] of Object.entries(entries)) {
      if (!knownMessageKeys.has(rawKey)) {
        throw new CaptureLocalisationError(`The capture catalogue contains unknown key ${rawKey}.`);
      }
      if (typeof value !== "string" || value.trim().length === 0) {
        throw new CaptureLocalisationError(
          `The capture catalogue value for ${locale}.${rawKey} must not be empty.`,
        );
      }
      const key = rawKey as CaptureMessageKey;
      const expectedPlaceholders = placeholders(englishMessages[key]);
      const actualPlaceholders = placeholders(value);
      if (!sameStrings(expectedPlaceholders, actualPlaceholders)) {
        throw new CaptureLocalisationError(
          `The capture catalogue value for ${locale}.${rawKey} must preserve placeholders: ${expectedPlaceholders.join(", ") || "none"}.`,
        );
      }
      messages[key] = value;
    }
    normalized.set(locale, messages);
  }
  return normalized;
}

function placeholders(template: string): readonly string[] {
  return [...template.matchAll(/\{([A-Za-z][A-Za-z0-9]*)\}/g)].map((match) => match[1]!).sort();
}

function sameStrings(left: readonly string[], right: readonly string[]): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

function interpolate(template: string, values: CaptureMessageValues): string {
  return template.replaceAll(/\{([A-Za-z][A-Za-z0-9]*)\}/g, (placeholder, key: string) => {
    const value = values[key];
    return value === undefined ? placeholder : String(value);
  });
}
