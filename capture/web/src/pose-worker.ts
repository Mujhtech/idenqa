import { FaceLandmarker, FilesetResolver } from "@mediapipe/tasks-vision";
import type { CaptureFacePose } from "./pose.js";

let tracker: FaceLandmarker | undefined;
// Classic worker permits MediaPipe's local WASM loader to use importScripts.
self.onmessage = async ({ data }) => {
  try {
    if (data.type === "init") {
      const files = await FilesetResolver.forVisionTasks(new URL("wasm/", data.base).href);
      tracker = await FaceLandmarker.createFromOptions(files, {
        baseOptions: {
          modelAssetPath: new URL("face_landmarker.task", data.base).href,
          delegate: "CPU",
        },
        runningMode: "IMAGE",
        numFaces: 2,
        minFaceDetectionConfidence: 0.7,
        minFacePresenceConfidence: 0.7,
        minTrackingConfidence: 0.7,
        outputFaceBlendshapes: true,
        outputFacialTransformationMatrixes: true,
      });
      self.postMessage({ ready: true });
      return;
    }
    const bitmap = data.bitmap as ImageBitmap;
    try {
      if (!tracker) throw new Error("Tracker not initialised");
      const result = tracker.detect(bitmap);
      const face = result.faceLandmarks[0];
      const m = result.facialTransformationMatrixes[0]?.data;
      const blend = result.faceBlendshapes[0]?.categories;
      const radians = 180 / Math.PI;
      const xs = face?.map((p) => p.x) ?? [0];
      const ys = face?.map((p) => p.y) ?? [0];
      const pose: CaptureFacePose = {
        faceCount: result.faceLandmarks.length,
        // MediaPipe's metric matrix is column-major, camera coordinates +Y up,
        // +Z toward the camera. Convert yaw to subject-right-positive.
        yaw: m ? -Math.atan2(m[8]!, m[10]!) * radians : NaN,
        pitch: m ? Math.atan2(-m[9]!, Math.hypot(m[8]!, m[10]!)) * radians : NaN,
        roll: m ? Math.atan2(m[1]!, m[0]!) * radians : NaN,
        centerX: (Math.min(...xs) + Math.max(...xs)) / 2,
        centerY: (Math.min(...ys) + Math.max(...ys)) / 2,
        width: Math.max(...xs) - Math.min(...xs),
        height: Math.max(...ys) - Math.min(...ys),
        leftEyeClosed: blend?.find((c) => c.categoryName === "eyeBlinkLeft")?.score ?? NaN,
        rightEyeClosed: blend?.find((c) => c.categoryName === "eyeBlinkRight")?.score ?? NaN,
      };
      self.postMessage({ pose });
    } finally {
      bitmap.close();
    }
  } catch {
    self.postMessage({ error: true });
  }
};
