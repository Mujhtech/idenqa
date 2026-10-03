import interVariableURL from "@fontsource-variable/inter/files/inter-latin-wght-normal.woff2";

let installed = false;

export function installCaptureFont(): void {
  if (installed || typeof FontFace === "undefined" || globalThis.document?.fonts === undefined) {
    return;
  }

  installed = true;
  const inter = new FontFace("Inter", `url(${JSON.stringify(interVariableURL)}) format("woff2")`, {
    display: "swap",
    style: "normal",
    weight: "100 900",
  });
  document.fonts.add(inter);
  void inter.load().catch(() => undefined);
}
