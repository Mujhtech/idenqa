import { acquisitionStyles } from "./acquisition-styles.js";
import { baseStyles } from "./base-styles.js";
import { journeyStyles } from "./journey-styles.js";
import { motionStyles } from "./motion-styles.js";
import { responsiveStyles } from "./responsive-styles.js";

// Preserve cascade order across the extracted stylesheets.
export const captureElementStyles = [
  baseStyles,
  acquisitionStyles,
  journeyStyles,
  motionStyles,
  responsiveStyles,
];
