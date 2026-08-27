import { defineConfig } from "tsdown";

export default defineConfig({
  entry: ["src/index.ts"],
  clean: true,
  dts: true,
  format: ["esm", "cjs"],
  platform: "neutral",
  sourcemap: true,
  target: "es2022",
});
