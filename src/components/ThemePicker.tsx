"use client";

import { useEffect, useRef, useState, type ComponentType } from "react";
import {
  BookOpen,
  Circle,
  Contrast,
  Feather,
  FileText,
  Gamepad2,
  Leaf,
  Monitor,
  Moon,
  Palette,
  Sticker,
  Sun,
  type LucideProps,
} from "lucide-react";
import { useFont } from "@/components/FontProvider";
import {
  THEME_MODES,
  THEME_PRESETS,
  type ThemeMode,
  type ThemePreset,
} from "@/lib/themes";

type ThemeIcon = ComponentType<LucideProps>;

const themeIcons: Record<ThemePreset, ThemeIcon> = {
  paper: FileText,
  sepia: BookOpen,
  ink: Feather,
  forest: Leaf,
  midnight: Moon,
  oled: Circle,
  contrast: Contrast,
  pixel: Gamepad2,
  toon: Sticker,
};

const themeIconColors: Record<ThemePreset, string> = {
  paper: "bg-[#f6efe3] text-[#a65335]",
  sepia: "bg-[#ead9b8] text-[#8a5527]",
  ink: "bg-[#e9ebee] text-[#23272d]",
  forest: "bg-[#dce8d8] text-[#476a4d]",
  midnight: "bg-[#dbe5f5] text-[#334d78]",
  oled: "bg-[#000000] text-[#ffffff]",
  contrast:
    "bg-[#ffffff] text-[#000000] ring-1 ring-inset ring-[#000000]",
  pixel: "bg-[#fff3bd] text-[#17234a] ring-2 ring-inset ring-[#d23b38]",
  toon: "bg-[#ffd4e6] text-[#c82d75] ring-2 ring-inset ring-[#593268]",
};

const modeIcons: Record<ThemeMode, ThemeIcon> = {
  light: Sun,
  dark: Moon,
  system: Monitor,
};

export function ThemePicker({ className = "" }: { className?: string }) {
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const {
    themePreset,
    setThemePreset,
    themeMode,
    setThemeMode,
    appearanceReady,
  } = useFont();
  const CurrentIcon = appearanceReady ? themeIcons[themePreset] : Palette;
  const currentLabel =
    THEME_PRESETS.find(({ key }) => key === themePreset)?.label || "纸张";
  const triggerLabel = appearanceReady
    ? `选择主题，当前为${currentLabel}`
    : "选择主题";

  useEffect(() => {
    if (!open) return;
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
        triggerRef.current?.focus();
      }
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [open]);

  return (
    <>
      {open && (
        <div
          aria-hidden="true"
          className="fixed inset-0 z-40 cursor-default"
          onClick={() => {
            setOpen(false);
            triggerRef.current?.focus();
          }}
        />
      )}
      <div className={`relative z-50 ${className}`}>
        <button
          ref={triggerRef}
          type="button"
          aria-label={triggerLabel}
          aria-expanded={open}
          aria-controls="mdhub-theme-picker"
          title={appearanceReady ? `主题：${currentLabel}` : "选择主题"}
          onClick={() => setOpen((value) => !value)}
          className={`inline-flex size-11 items-center justify-center rounded-full border transition-colors lg:size-9 ${
            open
              ? "border-stone-400 bg-stone-100 text-stone-900"
              : "border-stone-200 bg-white text-stone-600 hover:bg-stone-50 hover:text-stone-900"
          }`}
        >
          <CurrentIcon size={18} strokeWidth={1.8} aria-hidden="true" />
        </button>

        {open && (
          <div
            id="mdhub-theme-picker"
            role="group"
            aria-label="主题风格"
            className="absolute right-0 top-[calc(100%+0.65rem)] w-60 rounded-2xl border border-stone-200 bg-white p-3 shadow-xl"
          >
            <div className="grid grid-cols-3 place-items-center gap-2">
              {THEME_PRESETS.map(({ key, label }) => {
                const Icon = themeIcons[key];
                const selected = key === themePreset;
                return (
                  <button
                    key={key}
                    type="button"
                    aria-label={label}
                    aria-pressed={selected}
                    title={label}
                    onClick={() => setThemePreset(key)}
                    className={`relative inline-flex size-11 items-center justify-center rounded-xl transition-transform hover:scale-105 ${
                      themeIconColors[key]
                    } ${
                      selected
                        ? "outline-2 outline-offset-2 outline-[var(--accent)]"
                        : "outline-transparent"
                    }`}
                  >
                    <Icon
                      size={20}
                      strokeWidth={1.8}
                      fill={key === "oled" ? "currentColor" : "none"}
                      aria-hidden="true"
                    />
                  </button>
                );
              })}
            </div>

            <div className="mt-3 grid grid-cols-3 gap-1 border-t border-stone-100 pt-3">
              {THEME_MODES.map(({ key, label }) => {
                const Icon = modeIcons[key];
                const selected = key === themeMode;
                return (
                  <button
                    key={key}
                    type="button"
                    aria-label={label}
                    aria-pressed={selected}
                    title={label}
                    onClick={() => setThemeMode(key)}
                    className={`inline-flex min-h-11 min-w-11 items-center justify-center rounded-lg transition-colors ${
                      selected
                        ? "bg-stone-800 text-white"
                        : "bg-stone-100 text-stone-500 hover:bg-stone-200 hover:text-stone-800"
                    }`}
                  >
                    <Icon size={17} strokeWidth={1.8} aria-hidden="true" />
                  </button>
                );
              })}
            </div>
          </div>
        )}
      </div>
    </>
  );
}
