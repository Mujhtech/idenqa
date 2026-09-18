/** Display only server-redacted PNGs fetched through an authenticated tenant backend. */
export interface ReviewDisplay {
  readonly bytes: Uint8Array;
  readonly expiresAt: string;
}
export interface ReviewViewer {
  show(load: (signal: AbortSignal) => Promise<ReviewDisplay>): Promise<void>;
  clear(): void;
  destroy(): void;
}
/** Watermarks and event handling deter copying; browsers cannot prevent screenshots. */
export function createReviewViewer(container: HTMLElement): ReviewViewer {
  const document = container.ownerDocument;
  const image = document.createElement("img");
  image.alt = "Redacted evidence for the current review case";
  image.draggable = false;
  image.style.maxWidth = "100%";
  const status = document.createElement("p");
  status.setAttribute("role", "status");
  container.append(image, status);
  let url: string | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let controller: AbortController | undefined;
  let destroyed = false;
  const clear = () => {
    controller?.abort();
    controller = undefined;
    if (timer !== undefined) clearTimeout(timer);
    image.removeAttribute("src");
    if (url !== undefined) URL.revokeObjectURL(url);
    url = undefined;
    status.textContent = "Evidence display cleared. Request access to view again.";
  };
  const obscure = () => {
    clear();
  };
  const hidden = () => {
    if (document.hidden) clear();
  };
  const prevent = (event: Event) => event.preventDefault();
  const key = (event: KeyboardEvent) => {
    if (event.key === "PrintScreen") clear();
  };
  document.addEventListener("visibilitychange", hidden);
  document.defaultView?.addEventListener("blur", obscure);
  document.addEventListener("keyup", key);
  image.addEventListener("contextmenu", prevent);
  image.addEventListener("copy", prevent);
  return {
    clear,
    async show(load) {
      if (destroyed) throw new Error("Review viewer has been destroyed.");
      clear();
      const pending = new AbortController();
      controller = pending;
      status.textContent = "Loading evidence…";
      let bytes: Uint8Array | undefined;
      try {
        const display = await load(pending.signal);
        bytes = display.bytes;
        if (pending.signal.aborted || controller !== pending || destroyed) return;
        const remaining = Math.min(Date.parse(display.expiresAt) - Date.now(), 5 * 60 * 1000);
        if (!Number.isFinite(remaining) || remaining <= 0 || document.hidden)
          throw new Error("Evidence access expired.");
        const signature = [137, 80, 78, 71, 13, 10, 26, 10];
        if (
          bytes.length > 50 * 1024 * 1024 ||
          !signature.every((value, index) => bytes?.[index] === value)
        )
          throw new Error("Invalid evidence image.");
        url = URL.createObjectURL(new Blob([new Uint8Array(bytes)], { type: "image/png" }));
        image.src = url;
        status.textContent = "Evidence access is temporary and audited.";
        timer = setTimeout(clear, remaining);
      } catch (error) {
        if (controller === pending) {
          clear();
          status.textContent = "Evidence could not be displayed. Request access again.";
        }
        throw error;
      } finally {
        bytes?.fill(0);
      }
    },
    destroy() {
      destroyed = true;
      clear();
      document.removeEventListener("visibilitychange", hidden);
      document.defaultView?.removeEventListener("blur", obscure);
      document.removeEventListener("keyup", key);
      image.removeEventListener("contextmenu", prevent);
      image.removeEventListener("copy", prevent);
      image.remove();
      status.remove();
    },
  };
}
