import { defineConfig, loadEnv } from "vite";

import { createCaptureBootstrapPlugin } from "./demo/capture-bootstrap.mjs";

export default defineConfig(({ mode }) => {
  const environment = loadEnv(mode, process.cwd(), "IDENQA_CAPTURE_");
  const coreUrl = normalizedCoreUrl(environment.IDENQA_CAPTURE_BOOTSTRAP_CORE_URL);

  return {
    server: {
      // Conformance must keep its loaded module graph stable while other work
      // rebuilds workspace packages; hot reload would silently restart sessions.
      ...(environment.IDENQA_CAPTURE_FIXTURE_CONFORMANCE === "true"
        ? { hmr: false, watch: null }
        : {}),
      headers: {
        "Referrer-Policy": "no-referrer",
        "X-Content-Type-Options": "nosniff",
      },
      ...(coreUrl === undefined
        ? {}
        : {
            proxy: {
              "/core": {
                target: coreUrl,
                changeOrigin: true,
                ws: true,
                rewrite: (path) => path.replace(/^\/core/, ""),
              },
            },
          }),
    },
    plugins: [
      createCaptureBootstrapPlugin({
        coreUrl,
        apiKey: environment.IDENQA_CAPTURE_BOOTSTRAP_TENANT_API_KEY,
        region: environment.IDENQA_CAPTURE_BOOTSTRAP_REGION,
        launchCredential: environment.IDENQA_CAPTURE_BOOTSTRAP_LAUNCH_CREDENTIAL,
        controller: environment.IDENQA_CAPTURE_BOOTSTRAP_CONTROLLER,
        recipient: environment.IDENQA_CAPTURE_BOOTSTRAP_RECIPIENT,
      }),
    ],
  };
});

function normalizedCoreUrl(value) {
  if (value === undefined || value.trim() === "") return undefined;
  const url = new URL(value);
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new Error("IDENQA_CAPTURE_BOOTSTRAP_CORE_URL must use HTTP or HTTPS");
  }
  url.pathname = `${url.pathname.replace(/\/+$/, "")}/`;
  url.search = "";
  url.hash = "";
  return url.href;
}
