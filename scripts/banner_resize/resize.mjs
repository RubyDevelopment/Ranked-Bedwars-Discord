// Converts the HTML banners of a banner document (a backup written by the Go script next to this file) from 1920x1080 to 1400x800.
//
//   node scripts/banner_resize/resize.mjs <backup.json> <converted.json> [width] [height]
//
// Every pixel length in the banner's CSS and in the style="" attributes of its HTML is scaled: widths, left, right, horizontal margins,
// padding and letter spacing by the horizontal factor; heights, top, bottom, vertical margins and padding by the vertical factor (the
// two differ a little, 1920x1080 and 1400x800 are not the same shape). Font sizes, corner radii, shadows and line heights follow the
// horizontal factor so text and shapes keep their proportions. Hairline borders (up to 1.5px) are kept as they are, so thin lines do not
// turn faint; thicker borders are scaled and rounded to half a pixel.
import { readFileSync, writeFileSync } from "node:fs";

const [from, to, w = "1400", h = "800"] = process.argv.slice(2);
if (!from || !to) {
  console.error("usage: node scripts/banner_resize/resize.mjs <backup.json> <converted.json> [width] [height]");
  process.exit(1);
}
const NEW_W = Number(w), NEW_H = Number(h);

const PX = /(-?\d*\.?\d+)px/g;
const round = (n) => Math.round(n * 100) / 100;
const scale = (value, factor) => value.replace(PX, (_m, n) => `${round(Number(n) * factor)}px`);

const HORIZONTAL = new Set(["left", "right", "width", "min-width", "max-width", "margin-left", "margin-right", "padding-left", "padding-right", "column-gap", "letter-spacing", "text-indent", "flex-basis"]);
const VERTICAL = new Set(["top", "bottom", "height", "min-height", "max-height", "margin-top", "margin-bottom", "padding-top", "padding-bottom", "row-gap"]);
const SHAPE = new Set(["font-size", "border-radius", "box-shadow", "text-shadow", "line-height"]); // scale with the horizontal factor
const BORDER = /^border(-top|-right|-bottom|-left)?(-width)?$/;

/** The values of a shorthand with 1-4 parts: (top, right, bottom, left), each scaled by the factor of its direction. */
function boxShorthand(value, x, y) {
  const parts = value.trim().split(/\s+/);
  const t = parts[0], r = parts[1] ?? parts[0], b = parts[2] ?? parts[0], l = parts[3] ?? parts[1] ?? parts[0];
  const out = [scale(t, y), scale(r, x), scale(b, y), scale(l, x)];
  return parts.length === 1 ? (scale(t, y) === scale(t, x) ? out[0] : out.join(" ")) : parts.length === 2 ? `${out[0]} ${out[1]}` : parts.length === 3 ? `${out[0]} ${out[1]} ${out[2]}` : out.join(" ");
}

/** A border width: hairlines stay, thicker ones scale (to the nearest half pixel). A transparent spacer border scales like any height. */
function border(prop, value, x, y) {
  if (/rgba\(\s*0\s*,\s*0\s*,\s*0\s*,\s*0\s*\)|transparent/.test(value)) return scale(value, /top|bottom/.test(prop) ? y : x);
  return value.replace(PX, (_m, n) => {
    const width = Number(n);
    return `${width <= 1.5 ? width : Math.max(1, Math.round(width * x * 2) / 2)}px`;
  });
}

export function convertDeclaration(prop, value, x, y) {
  if (!PX.test(value)) { PX.lastIndex = 0; return value; }
  PX.lastIndex = 0;
  if (prop === "margin" || prop === "padding") return boxShorthand(value, x, y);
  if (BORDER.test(prop)) return border(prop, value, x, y);
  if (HORIZONTAL.has(prop) || SHAPE.has(prop)) return scale(value, x);
  if (VERTICAL.has(prop)) return scale(value, y);
  throw new Error(`unhandled property with pixels: ${prop}: ${value}`);
}

/** Rewrites every `property: value` of a CSS text or of a style="" attribute. */
function convertCss(css, x, y) {
  return css.replace(/([a-z-]+)(\s*:\s*)([^;{}"]+)/g, (whole, prop, colon, value) => {
    // Only declarations: a selector never has a pixel length after a colon in this CSS, and a value without px is left alone.
    if (!/px/.test(value)) return whole;
    return `${prop}${colon}${convertDeclaration(prop, value, x, y)}`;
  });
}

const doc = JSON.parse(readFileSync(from, "utf8"));
let converted = 0;
for (const banner of doc.banners) {
  if (banner.mode !== "html") continue;
  const x = NEW_W / banner.width, y = NEW_H / banner.height;
  banner.css = convertCss(banner.css, x, y);
  banner.html = banner.html.replace(/style="([^"]*)"/g, (_m, style) => `style="${convertCss(style, x, y)}"`);
  banner.width = NEW_W;
  banner.height = NEW_H;
  converted++;
}
writeFileSync(to, JSON.stringify(doc));
console.log(`converted ${converted} banners to ${NEW_W}x${NEW_H}: ${to}`);
