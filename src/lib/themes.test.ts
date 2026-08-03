import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  resolveThemeAppearance,
  THEME_PRESETS,
} from "./themes.ts";

function relativeLuminance(hex: string): number {
  const channels = hex
    .slice(1)
    .match(/../g)!
    .map((channel) => Number.parseInt(channel, 16) / 255)
    .map((channel) =>
      channel <= 0.04045
        ? channel / 12.92
        : ((channel + 0.055) / 1.055) ** 2.4,
    );
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
}

function contrastRatio(first: string, second: string): number {
  const firstLuminance = relativeLuminance(first);
  const secondLuminance = relativeLuminance(second);
  return (
    (Math.max(firstLuminance, secondLuminance) + 0.05) /
    (Math.min(firstLuminance, secondLuminance) + 0.05)
  );
}

function themeTokens(
  css: string,
  theme: "pixel" | "toon",
  dark: boolean,
): Record<string, string> {
  const selector = `html\\[data-theme="${theme}"\\]${dark ? "\\.dark" : ""}`;
  const block = css.match(new RegExp(`${selector} \\{([\\s\\S]*?)\\n\\}`))?.[1];
  assert.ok(block, `missing ${theme} ${dark ? "dark" : "light"} token block`);
  return Object.fromEntries(
    [...block.matchAll(/--([\w-]+):\s*(#[0-9a-f]{6});/gi)].map((match) => [
      match[1],
      match[2],
    ]),
  );
}

test("theme appearance validates stored preferences and follows system mode", () => {
  assert.deepEqual(resolveThemeAppearance("sepia", "system", true), {
    preset: "sepia",
    mode: "system",
    dark: true,
  });
  assert.deepEqual(resolveThemeAppearance("unknown", "broken", false), {
    preset: "paper",
    mode: "system",
    dark: false,
  });
});

test("theme catalog exposes distinct reader styles", () => {
  assert.deepEqual(
    THEME_PRESETS.map((theme) => theme.key),
    [
      "paper",
      "sepia",
      "ink",
      "forest",
      "midnight",
      "oled",
      "contrast",
      "pixel",
      "toon",
    ],
  );
  assert.equal(new Set(THEME_PRESETS.map((theme) => theme.label)).size, THEME_PRESETS.length);
});

test("every non-default theme has light and dark token definitions", () => {
  const css = readFileSync(new URL("../app/globals.css", import.meta.url), "utf8");
  for (const { key } of THEME_PRESETS) {
    if (key === "paper") continue;
    assert.match(css, new RegExp(`data-theme="${key}"\\] \\{`));
    assert.match(css, new RegExp(`data-theme="${key}"\\]\\.dark \\{`));
  }
});

test("playful themes preserve readable text and hover contrast", () => {
  const css = readFileSync(new URL("../app/globals.css", import.meta.url), "utf8");
  for (const theme of ["pixel", "toon"] as const) {
    for (const dark of [false, true]) {
      const tokens = themeTokens(css, theme, dark);
      const pairs = [
        ["foreground", "background"],
        ["accent", "background"],
        ["accent", "color-white"],
        ["color-stone-400", "color-white"],
        ["color-stone-600", "color-stone-200"],
        ["color-stone-800", "color-white"],
      ] as const;
      for (const [foreground, background] of pairs) {
        const ratio = contrastRatio(tokens[foreground], tokens[background]);
        assert.ok(
          ratio >= 4.5,
          `${theme} ${dark ? "dark" : "light"} ${foreground}/${background} contrast is ${ratio.toFixed(2)}`,
        );
      }
    }
  }
});
