import { describe, expect, it } from "vitest";
import { CaptureClient, IdenqaProtocolError, type ExperienceAsset } from "../src/index.js";

const body = new Uint8Array([1, 2, 3]);
const asset: ExperienceAsset = {
  key: "brand-logo",
  kind: "image",
  mime: "image/png",
  size: 3,
  digest: "039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81",
  object_key: "experiences/ten_test/approved/logo",
  object_version: "v1",
};

describe("pinned experience image delivery", () => {
  it("uses a header token, omits ambient credentials and verifies the bounded image", async () => {
    const client = new CaptureClient({
      baseUrl: "https://core.example",
      captureToken: "capture-test",
      fetch: async (url, init) => {
        expect(String(url)).toBe("https://core.example/v1/capture/experience/assets/brand-logo");
        expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer capture-test");
        expect(init).toMatchObject({
          credentials: "omit",
          redirect: "error",
          cache: "no-store",
          referrerPolicy: "no-referrer",
        });
        return new Response(body.slice(), {
          headers: {
            "X-Request-ID": "req_image",
            "Content-Type": "image/png",
            "Content-Length": "3",
          },
        });
      },
    });
    const response = await client.getExperienceAsset(asset);
    expect(response.requestId).toBe("req_image");
    expect(response.data.type).toBe("image/png");
    expect(new Uint8Array(await response.data.arrayBuffer())).toEqual(body);
  });
  it.each([
    {
      name: "digest mismatch",
      value: new Uint8Array([1, 2, 4]),
      mime: "image/png",
      requestId: "req_image",
    },
    { name: "truncated", value: new Uint8Array([1, 2]), mime: "image/png", requestId: "req_image" },
    {
      name: "overlong",
      value: new Uint8Array([1, 2, 3, 4]),
      mime: "image/png",
      requestId: "req_image",
    },
    { name: "wrong MIME", value: body.slice(), mime: "image/svg+xml", requestId: "req_image" },
    { name: "missing request identity", value: body.slice(), mime: "image/png", requestId: "" },
  ])("refuses $name before exposing a blob", async ({ value, mime, requestId }) => {
    const client = new CaptureClient({
      baseUrl: "https://core.example",
      captureToken: "capture-test",
      fetch: async () =>
        new Response(value, { headers: { "Content-Type": mime, "X-Request-ID": requestId } }),
    });
    await expect(client.getExperienceAsset(asset)).rejects.toThrow(IdenqaProtocolError);
  });
});
