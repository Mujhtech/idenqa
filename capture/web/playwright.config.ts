import { defineConfig, devices } from "@playwright/test";

const externalBaseURL = process.env.IDENQA_CAPTURE_LIVE_DEMO_URL;

export default defineConfig({
  testDir: "./test/browser",
  fullyParallel: true,
  reporter: "line",
  use: {
    baseURL: externalBaseURL ?? "http://127.0.0.1:4173",
    trace: "retain-on-failure",
  },
  ...(externalBaseURL === undefined
    ? {
        webServer: {
          command:
            "pnpm exec vite ./demo --config ./vite.config.mjs --host 127.0.0.1 --port 4173 --strictPort",
          reuseExistingServer: false,
          url: "http://127.0.0.1:4173",
        },
      }
    : {}),
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        launchOptions: {
          args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream"],
        },
      },
    },
  ],
});
