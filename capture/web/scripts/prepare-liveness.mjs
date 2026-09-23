import { createHash } from "node:crypto";
import { mkdir, copyFile, cp, readFile, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../", import.meta.url));
const destination = resolve(process.argv[2] ?? resolve(root, "demo/public/idenqa-liveness"));
const expected = "64184e229b263107bc2b804c6625db1341ff2bb731874b0bcc2fe6544e0bc9ff";
const url =
  "https://storage.googleapis.com/mediapipe-models/face_landmarker/face_landmarker/float16/1/face_landmarker.task";
const model = process.argv[3]
  ? await readFile(process.argv[3])
  : Buffer.from(
      await (async () => {
        const response = await fetch(url, { signal: AbortSignal.timeout(60_000) });
        if (!response.ok) throw new Error(`Model download failed: ${response.status}`);
        return response.arrayBuffer();
      })(),
    );
if (createHash("sha256").update(model).digest("hex") !== expected)
  throw new Error("Face model digest does not match the pinned release.");
const require = createRequire(import.meta.url);
const library = dirname(require.resolve("@mediapipe/tasks-vision"));
await mkdir(destination, { recursive: true });
await copyFile(resolve(root, "dist/pose-worker.js"), resolve(destination, "pose-worker.js"));
await cp(resolve(library, "wasm"), resolve(destination, "wasm"), { recursive: true });
await copyFile(
  resolve(root, "THIRD_PARTY_NOTICES.md"),
  resolve(destination, "THIRD_PARTY_NOTICES.md"),
);
await writeFile(resolve(destination, "face_landmarker.task"), model);
console.log(`Prepared same-origin liveness assets in ${destination}`);
