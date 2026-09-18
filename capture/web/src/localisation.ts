export const CAPTURE_MESSAGE_KEYS = [
  "secureCapture",
  "identityVerification",
  "introTitle",
  "introBody",
  "introStepCount",
  "introPrivate",
  "introDevice",
  "getStarted",
  "cancel",
  "back",
  "continue",
  "chooseAnotherMethod",
  "preparingSecureCapture",
  "preparingCaptureBody",
  "capturePreparationFailedTitle",
  "capturePreparationFailed",
  "captureCancelledTitle",
  "captureCancelled",
  "preparingCapture",
  "whyInformationNeeded",
  "noticeStep",
  "noticeTitle",
  "noticeBody",
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
  "chooseMethod",
  "chooseMethodTitle",
  "chooseMethodBody",
  "cameraMethodDescription",
  "fileMethodDescription",
  "otherMethodDescription",
  "prepare",
  "prepareTitle",
  "cameraPreparation",
  "filePreparation",
  "tipLighting",
  "tipReadable",
  "tipPrivacy",
  "capture",
  "captureTitle",
  "cameraInstruction",
  "fileInstruction",
  "otherInstruction",
  "adapterPreviewLabel",
  "adapterPreparing",
  "adapterRequestingPermission",
  "adapterReady",
  "adapterRunning",
  "adapterSubmitting",
  "adapterFailed",
  "adapterUnconfirmed",
  "cancelMethod",
  "challengeProgress",
  "livenessProgressLabel",
  "livenessAutoCapture",
  "livenessNeutral",
  "livenessTurnLeft",
  "livenessTurnRight",
  "livenessLookUp",
  "livenessLookDown",
  "livenessBlink",
  "stepOf",
  "progressPercent",
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
  "selectedFilePreview",
  "reviewSelectedFile",
  "useFile",
  "chooseDifferentFile",
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
  "adapterStepComplete",
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
  "recovery",
  "recoveryTitle",
  "recoveryBody",
  "savedProgress",
  "savedProgressBody",
  "resumeCapture",
  "saved",
  "confirmationTitle",
  "confirmationBody",
  "securelyUploaded",
  "nextItem",
  "reviewAndFinish",
  "processing",
  "processingTitle",
  "processingBody",
  "verificationSuccess",
  "verificationSuccessTitle",
  "verificationSuccessBody",
  "actionRequired",
  "actionRequiredTitle",
  "actionRequiredBody",
  "verificationUnsuccessful",
  "verificationUnsuccessfulTitle",
  "verificationUnsuccessfulBody",
  "verificationInconclusiveTitle",
  "verificationInconclusiveBody",
  "verificationExpiredTitle",
  "verificationExpiredBody",
  "verificationFailedTitle",
  "verificationFailedBody",
  "verificationCancelledBody",
  "complete",
  "completeTitle",
  "completeBody",
  "closePage",
  "refusalTitle",
  "authorityBlockedTitle",
  "requiredItem",
  "selfie",
  "documentFront",
  "documentBack",
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
  introTitle: "Let’s Verify Your Identity",
  introBody:
    "We’ll guide you through each step. Have your identity document nearby and make sure you’re in a well-lit place.",
  introStepCount: "Items to capture: {count}",
  introPrivate: "Your information is encrypted and sent securely",
  introDevice: "Works on your phone, tablet, or computer",
  getStarted: "Get Started",
  cancel: "Cancel",
  back: "Back",
  continue: "Continue to Capture",
  chooseAnotherMethod: "Use Another Method",
  preparingSecureCapture: "Preparing secure capture…",
  preparingCaptureBody: "This should only take a moment.",
  capturePreparationFailedTitle: "We Couldn’t Open This Verification",
  capturePreparationFailed: "Capture could not be prepared. Check the capture link and try again.",
  captureCancelledTitle: "Capture Cancelled",
  captureCancelled: "Capture cancelled. No evidence was collected.",
  preparingCapture: "Preparing capture…",
  whyInformationNeeded: "Why Your Information Is Needed",
  noticeStep: "Your Privacy",
  noticeTitle: "Review Before You Continue",
  noticeBody: "Please read how your information will be used, then choose whether to continue.",
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
  chooseMethod: "Choose a Method",
  chooseMethodTitle: "How Would You Like to Add {item}?",
  chooseMethodBody: "Choose the option that works best on this device.",
  cameraMethodDescription: "Take a new photo now",
  fileMethodDescription: "Choose an existing image",
  otherMethodDescription: "Continue with this approved method",
  prepare: "Get Ready",
  prepareTitle: "Get {item} Ready",
  cameraPreparation: "Before opening the camera, take a moment to set up a clear shot.",
  filePreparation: "Choose a clear, recent image where the whole required item is visible.",
  tipLighting: "Use bright, even lighting and avoid glare.",
  tipReadable: "Keep every edge visible and all details easy to read.",
  tipPrivacy: "Only include the item requested on this screen.",
  capture: "Capture",
  captureTitle: "Add {item}",
  cameraInstruction: "Position the item inside the frame, then take a photo.",
  fileInstruction: "Choose an image, review it, then confirm the upload.",
  otherInstruction: "Follow the instructions for this approved capture method.",
  adapterPreviewLabel: "Live preview for {method}",
  adapterPreparing: "Preparing {method}…",
  adapterRequestingPermission: "Waiting for permission to start {method}…",
  adapterReady: "{method} is ready.",
  adapterRunning: "{method} is in progress…",
  adapterSubmitting: "Securely submitting {method}…",
  adapterFailed: "This capture method did not finish. Check the instructions and retry.",
  adapterUnconfirmed:
    "The capture was not confirmed by the verification service. Check your connection and retry.",
  cancelMethod: "Cancel {method}",
  challengeProgress: "Liveness Prompt {current} of {total}",
  livenessProgressLabel: "Liveness challenge progress",
  livenessAutoCapture: "Automatic capture is on",
  livenessNeutral: "Look Straight at the Camera",
  livenessTurnLeft: "Slowly Turn Your Head Left",
  livenessTurnRight: "Slowly Turn Your Head Right",
  livenessLookUp: "Slowly Look Up",
  livenessLookDown: "Slowly Look Down",
  livenessBlink: "Blink Naturally",
  stepOf: "Step {current} of {total}",
  progressPercent: "{percent}% complete",
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
  selectedFilePreview: "Preview of {artefact}",
  reviewSelectedFile: "Make sure the image is clear and complete before using it.",
  useFile: "Use This File",
  chooseDifferentFile: "Choose a Different File",
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
  capturedPreviewLabel: "Captured Preview of {artefact}",
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
  adapterStepComplete: "{method} accepted. Capture step complete.",
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
  recovery: "Progress Restored",
  recoveryTitle: "Welcome Back",
  recoveryBody:
    "We found your secure progress. {completed} of {total} capture steps are already complete.",
  savedProgress: "Your completed steps are saved",
  savedProgressBody: "You won’t need to upload them again.",
  resumeCapture: "Resume Capture",
  saved: "Saved Securely",
  confirmationTitle: "{item} Added",
  confirmationBody: "That step is complete. You can continue when you’re ready.",
  securelyUploaded: "Uploaded securely",
  nextItem: "Continue to Next Step",
  reviewAndFinish: "Review and Finish",
  processing: "Final Check",
  processingTitle: "Verifying Your Identity",
  processingBody:
    "Your evidence was sent securely. Keep this page open while the final result is prepared.",
  verificationSuccess: "Verification Complete",
  verificationSuccessTitle: "Identity Verified",
  verificationSuccessBody: "Your identity verification was completed successfully.",
  actionRequired: "More Information Needed",
  actionRequiredTitle: "Check the Next Step",
  actionRequiredBody:
    "The organisation that requested this verification needs more information. Follow the instructions they send you, then return to this page if asked.",
  verificationUnsuccessful: "Verification Complete",
  verificationUnsuccessfulTitle: "We Couldn’t Verify Your Identity",
  verificationUnsuccessfulBody:
    "The verification finished without confirming your identity. Contact the organisation that requested the check if you need help.",
  verificationInconclusiveTitle: "We Couldn’t Complete the Verification",
  verificationInconclusiveBody:
    "The verification did not reach a conclusive result. Contact the organisation that requested the check for the next step.",
  verificationExpiredTitle: "This Verification Has Expired",
  verificationExpiredBody:
    "This capture link is no longer active. Ask the organisation that requested the check for a new link.",
  verificationFailedTitle: "We Couldn’t Complete the Verification",
  verificationFailedBody:
    "A technical problem prevented this verification from completing. Contact the organisation that requested the check.",
  verificationCancelledBody: "This verification was cancelled and cannot be continued.",
  complete: "Capture Complete",
  completeTitle: "You’re All Set",
  completeBody:
    "Your evidence was sent securely. The organisation that requested this check will let you know when verification is complete.",
  closePage: "You can safely close this page.",
  refusalTitle: "You Chose Not to Continue",
  authorityBlockedTitle: "Capture Is Unavailable",
  requiredItem: "Required Item",
  selfie: "Your Selfie",
  documentFront: "Front of Your Identity Document",
  documentBack: "Back of Your Identity Document",
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
