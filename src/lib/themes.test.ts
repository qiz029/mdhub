import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  resolveThemeAppearance,
  THEME_PRESETS,
} from "./themes.ts";

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
    ["paper", "sepia", "ink", "forest", "midnight", "oled", "contrast"],
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
