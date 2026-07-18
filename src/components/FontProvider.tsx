"use client";

import { createContext, useContext, useState, useEffect, type ReactNode } from "react";

export type FontPreset = "system" | "serif" | "kai" | "hei" | "wenkai" | "fangsong";

export const FONT_PRESETS: { key: FontPreset; label: string }[] = [
  { key: "system", label: "系统默认" },
  { key: "serif", label: "宋体" },
  { key: "kai", label: "楷体" },
  { key: "hei", label: "黑体" },
  { key: "wenkai", label: "霞鹜文楷" },
  { key: "fangsong", label: "仿宋" },
];

const STORAGE_KEY = "mdhub-font";

function readStored(): FontPreset {
  if (typeof window === "undefined") return "system";
  return (localStorage.getItem(STORAGE_KEY) as FontPreset) || "system";
}

function writeStored(preset: FontPreset) {
  localStorage.setItem(STORAGE_KEY, preset);
}

function applyClass(preset: FontPreset) {
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

type FontCtx = {
  font: FontPreset;
  setFont: (f: FontPreset) => void;
};

const Ctx = createContext<FontCtx>({ font: "system", setFont: () => {} });

export function useFont() {
  return useContext(Ctx);
}

export function FontProvider({ children }: { children: ReactNode }) {
  const [font, setFontState] = useState<FontPreset>("system");

  useEffect(() => {
    const stored = readStored();
    setFontState(stored);
    applyClass(stored);
  }, []);

  function setFont(next: FontPreset) {
    setFontState(next);
    writeStored(next);
    applyClass(next);
  }

  return <Ctx.Provider value={{ font, setFont }}>{children}</Ctx.Provider>;
}
