import { defineConfig } from "tsdown";

export default defineConfig([
  {
    entry: ["src/index.ts"],
    clean: true,
    deps: { neverBundle: ["@idenqa/sdk", "lit"] },
    dts: true,
    format: ["esm", "cjs"],
    platform: "neutral",
    sourcemap: true,
    target: "es2022",
  },
  {
    entry: { "pose-worker": "src/pose-worker.ts" },
    outDir: "dist",
    clean: false,
    dts: false,
    format: "iife",
    platform: "browser",
    target: "es2022",
    sourcemap: true,
    deps: { alwaysBundle: ["@mediapipe/tasks-vision"] },
    outputOptions: { entryFileNames: "pose-worker.js" },
  },
]);
