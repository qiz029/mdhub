"use client";

import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import {
  resolveThemeAppearance,
  type ThemeMode,
  type ThemePreset,
} from "@/lib/themes";

export type FontPreset = "system" | "serif" | "kai" | "hei" | "wenkai" | "fangsong";
export type FontSize = "sm" | "md" | "lg" | "xl";
export type ContentWidth = "narrow" | "normal" | "wide" | "full";
export const FONT_PRESETS: { key: FontPreset; label: string }[] = [
  { key: "system", label: "系统默认" },
  { key: "serif", label: "宋体" },
  { key: "kai", label: "楷体" },
  { key: "hei", label: "黑体" },
  { key: "wenkai", label: "霞鹜文楷" },
  { key: "fangsong", label: "仿宋" },
];

export const FONT_SIZES: { key: FontSize; label: string; value: string }[] = [
  { key: "sm", label: "小", value: "0.9375rem" },
  { key: "md", label: "标准", value: "1.0625rem" },
  { key: "lg", label: "大", value: "1.1875rem" },
  { key: "xl", label: "特大", value: "1.3125rem" },
];

export const CONTENT_WIDTHS: { key: ContentWidth; label: string; value: string }[] = [
  { key: "narrow", label: "窄", value: "36rem" },
  { key: "normal", label: "标准", value: "42rem" },
  { key: "wide", label: "宽", value: "56rem" },
  { key: "full", label: "全宽", value: "none" },
];

const FONT_KEY = "mdhub-font";
const SIZE_KEY = "mdhub-font-size";
const WIDTH_KEY = "mdhub-width";
const THEME_PRESET_KEY = "mdhub-theme-preset";
const THEME_MODE_KEY = "mdhub-theme";

function readStored<T extends string>(key: string, fallback: T): T {
  if (typeof window === "undefined") return fallback;
  return (localStorage.getItem(key) as T) || fallback;
}

function applyFont(preset: FontPreset) {
  const root = document.documentElement;
  root.classList.remove(
    "font-system",
    "font-serif",
    "font-kai",
    "font-hei",
    "font-wenkai",
    "font-fangsong",
  );
  root.classList.add(`font-${preset}`);
}

function applyFontSize(size: FontSize) {
  const preset = FONT_SIZES.find((s) => s.key === size) || FONT_SIZES[1];
  document.documentElement.style.setProperty("--reader-font-size", preset.value);
}

function applyWidth(width: ContentWidth) {
  const preset = CONTENT_WIDTHS.find((w) => w.key === width) || CONTENT_WIDTHS[1];
  document.documentElement.style.setProperty("--reader-width", preset.value);
}

function applyTheme(preset: ThemePreset, mode: ThemeMode) {
  const appearance = resolveThemeAppearance(
    preset,
    mode,
    window.matchMedia("(prefers-color-scheme: dark)").matches,
  );
  document.documentElement.dataset.theme = appearance.preset;
  document.documentElement.classList.toggle("dark", appearance.dark);
}

type ReaderCtx = {
  font: FontPreset;
  setFont: (f: FontPreset) => void;
  fontSize: FontSize;
  setFontSize: (s: FontSize) => void;
  contentWidth: ContentWidth;
  setContentWidth: (w: ContentWidth) => void;
  themePreset: ThemePreset;
  setThemePreset: (theme: ThemePreset) => void;
  themeMode: ThemeMode;
  setThemeMode: (mode: ThemeMode) => void;
  appearanceReady: boolean;
};

const Ctx = createContext<ReaderCtx>({
  font: "system",
  setFont: () => {},
  fontSize: "md",
  setFontSize: () => {},
  contentWidth: "normal",
  setContentWidth: () => {},
  themePreset: "paper",
  setThemePreset: () => {},
  themeMode: "system",
  setThemeMode: () => {},
  appearanceReady: false,
});

export function useFont() {
  return useContext(Ctx);
}

export function FontProvider({ children }: { children: ReactNode }) {
  const [font, setFontState] = useState<FontPreset>("system");
  const [fontSize, setFontSizeState] = useState<FontSize>("md");
  const [contentWidth, setContentWidthState] = useState<ContentWidth>("normal");
  const [themePreset, setThemePresetState] = useState<ThemePreset>("paper");
  const [themeMode, setThemeModeState] = useState<ThemeMode>("system");
  const [appearanceReady, setAppearanceReady] = useState(false);

  useEffect(() => {
    const storedFont = readStored(FONT_KEY, "system");
    const storedSize = readStored(SIZE_KEY, "md");
    const storedWidth = readStored(WIDTH_KEY, "normal");
    const storedAppearance = resolveThemeAppearance(
      localStorage.getItem(THEME_PRESET_KEY),
      localStorage.getItem(THEME_MODE_KEY),
      window.matchMedia("(prefers-color-scheme: dark)").matches,
    );
    setFontState(storedFont);
    setFontSizeState(storedSize);
    setContentWidthState(storedWidth);
    setThemePresetState(storedAppearance.preset);
    setThemeModeState(storedAppearance.mode);
    applyFont(storedFont);
    applyFontSize(storedSize);
    applyWidth(storedWidth);
    applyTheme(storedAppearance.preset, storedAppearance.mode);
    setAppearanceReady(true);

    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const onSystemChange = () => {
      const preset = readStored<ThemePreset>(THEME_PRESET_KEY, "paper");
      if (readStored<ThemeMode>(THEME_MODE_KEY, "system") === "system") {
        applyTheme(preset, "system");
      }
    };
    mq.addEventListener("change", onSystemChange);
    return () => mq.removeEventListener("change", onSystemChange);
  }, []);

  function setFont(next: FontPreset) {
    setFontState(next);
    localStorage.setItem(FONT_KEY, next);
    applyFont(next);
  }

  function setFontSize(next: FontSize) {
    setFontSizeState(next);
    localStorage.setItem(SIZE_KEY, next);
    applyFontSize(next);
  }

  function setContentWidth(next: ContentWidth) {
    setContentWidthState(next);
    localStorage.setItem(WIDTH_KEY, next);
    applyWidth(next);
  }

  function setThemePreset(next: ThemePreset) {
    setThemePresetState(next);
    localStorage.setItem(THEME_PRESET_KEY, next);
    applyTheme(next, themeMode);
  }

  function setThemeMode(next: ThemeMode) {
    setThemeModeState(next);
    localStorage.setItem(THEME_MODE_KEY, next);
    applyTheme(themePreset, next);
  }

  return (
    <Ctx.Provider
      value={{
        font,
        setFont,
        fontSize,
        setFontSize,
        contentWidth,
        setContentWidth,
        themePreset,
        setThemePreset,
        themeMode,
        setThemeMode,
        appearanceReady,
      }}
    >
      {children}
    </Ctx.Provider>
  );
}
