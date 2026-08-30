import { defineConfig } from "tsdown";

export default defineConfig({
  entry: ["src/index.ts"],
  clean: true,
  deps: { neverBundle: ["@idenqa/sdk", "lit"] },
  dts: true,
  format: ["esm", "cjs"],
  platform: "neutral",
  sourcemap: true,
  target: "es2022",
});
