import { useEffect, useRef, useState, type DetailedHTMLProps, type HTMLAttributes } from "react";
import { createRoot } from "react-dom/client";

import {
  defineIdenqaCapture,
  type CaptureCompleteDetail,
  type CaptureMessageCatalogue,
  type IdenqaCaptureElement,
} from "../src/index.js";

defineIdenqaCapture();

interface ReactDemoStartInput {
  readonly captureToken: string;
  readonly methods?: readonly string[];
  readonly messageCatalogue?: CaptureMessageCatalogue;
}

declare global {
  interface Window {
    startIdenqaReactDemo(input: ReactDemoStartInput): Promise<void>;
  }
}

function CaptureHost() {
  const captureRef = useRef<IdenqaCaptureElement>(null);
  const [status, setStatus] = useState("React host is ready.");

  useEffect(() => {
    const capture = captureRef.current;
    if (capture === null) return;
    const completed = (event: Event) => {
      const detail = (event as CustomEvent<CaptureCompleteDetail>).detail;
      setStatus(`React host observed completion: ${detail.completedSteps}/${detail.totalSteps}.`);
    };
    capture.addEventListener("idenqa-capture-complete", completed);
    window.startIdenqaReactDemo = async (input) => {
      const methods = input.methods ?? ["idenqa.method.file_upload"];
      await capture.start({
        baseUrl: new URL("/core/", location.href),
        captureToken: input.captureToken,
        capabilities: {
          supportedMethods: [...methods],
          availableMethods: [...methods],
        },
        ...(input.messageCatalogue === undefined
          ? {}
          : { messageCatalogue: input.messageCatalogue }),
      });
    };
    return () => {
      capture.removeEventListener("idenqa-capture-complete", completed);
      delete (window as Partial<Window>).startIdenqaReactDemo;
    };
  }, []);

  return (
    <>
      <idenqa-capture ref={captureRef}></idenqa-capture>
      <output id="react-status" aria-live="polite">
        {status}
      </output>
    </>
  );
}

const root = document.querySelector<HTMLDivElement>("#root");
if (root === null) throw new Error("The React capture fixture is incomplete.");
createRoot(root).render(<CaptureHost />);

declare module "react" {
  namespace JSX {
    interface IntrinsicElements {
      "idenqa-capture": DetailedHTMLProps<
        HTMLAttributes<IdenqaCaptureElement>,
        IdenqaCaptureElement
      >;
    }
  }
}
