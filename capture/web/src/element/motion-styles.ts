import { css } from "lit";

export const motionStyles = css`
  @keyframes idq-pulse {
    0%,
    100% {
      opacity: 0.25;
      transform: translateY(0);
    }
    50% {
      opacity: 1;
      transform: translateY(-0.2rem);
    }
  }

  @keyframes idq-liveness-head-demo {
    0%,
    23%,
    48%,
    73%,
    100% {
      transform: translate3d(0, 0, 0) rotate(0) scaleX(1);
    }
    5%,
    17% {
      transform: translate3d(-0.35rem, 0, 0) rotate(-3deg) scaleX(0.96);
    }
    30%,
    42% {
      transform: translate3d(0.35rem, 0, 0) rotate(3deg) scaleX(0.96);
    }
    55%,
    67% {
      transform: translate3d(0, -0.35rem, 0) rotate(0) scaleX(1);
    }
    80%,
    92% {
      transform: translate3d(0, 0.35rem, 0) rotate(0) scaleX(1);
    }
  }

  @keyframes idq-liveness-gaze-demo {
    0%,
    23%,
    48%,
    73%,
    100% {
      transform: translate3d(0, 0, 0);
    }
    5%,
    17% {
      transform: translate3d(-0.22rem, 0, 0);
    }
    30%,
    42% {
      transform: translate3d(0.22rem, 0, 0);
    }
    55%,
    67% {
      transform: translate3d(0, -0.18rem, 0);
    }
    80%,
    92% {
      transform: translate3d(0, 0.18rem, 0);
    }
  }

  @keyframes idq-liveness-direction-left-demo {
    0%,
    23%,
    100% {
      opacity: var(--idq-capture-liveness-cue-opacity);
      transform: translate3d(0, 0, 0);
    }
    5%,
    17% {
      opacity: var(--idq-capture-liveness-cue-active-opacity);
      transform: translate3d(var(--idq-liveness-cue-x), var(--idq-liveness-cue-y), 0);
    }
  }

  @keyframes idq-liveness-direction-right-demo {
    0%,
    23%,
    48%,
    100% {
      opacity: var(--idq-capture-liveness-cue-opacity);
      transform: translate3d(0, 0, 0);
    }
    30%,
    42% {
      opacity: var(--idq-capture-liveness-cue-active-opacity);
      transform: translate3d(var(--idq-liveness-cue-x), var(--idq-liveness-cue-y), 0);
    }
  }

  @keyframes idq-liveness-direction-up-demo {
    0%,
    48%,
    73%,
    100% {
      opacity: var(--idq-capture-liveness-cue-opacity);
      transform: translate3d(0, 0, 0);
    }
    55%,
    67% {
      opacity: var(--idq-capture-liveness-cue-active-opacity);
      transform: translate3d(var(--idq-liveness-cue-x), var(--idq-liveness-cue-y), 0);
    }
  }

  @keyframes idq-liveness-direction-down-demo {
    0%,
    73%,
    100% {
      opacity: var(--idq-capture-liveness-cue-opacity);
      transform: translate3d(0, 0, 0);
    }
    80%,
    92% {
      opacity: var(--idq-capture-liveness-cue-active-opacity);
      transform: translate3d(var(--idq-liveness-cue-x), var(--idq-liveness-cue-y), 0);
    }
  }
`;
