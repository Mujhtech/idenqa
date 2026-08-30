import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    exclude: ["test/browser/**"],
    include: ["test/**/*.test.ts"],
  },
});
