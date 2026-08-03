export type ThemePreset =
  | "paper"
  | "sepia"
  | "ink"
  | "forest"
  | "midnight"
  | "oled"
  | "contrast"
  | "pixel"
  | "toon";

export type ThemeMode = "light" | "dark" | "system";

export type ThemeAppearance = {
  preset: ThemePreset;
  mode: ThemeMode;
  dark: boolean;
};

export const THEME_PRESETS: ReadonlyArray<{
  key: ThemePreset;
  label: string;
}> = [
  { key: "paper", label: "纸张" },
  { key: "sepia", label: "旧书" },
  { key: "ink", label: "墨水" },
  { key: "forest", label: "森林" },
  { key: "midnight", label: "午夜" },
  { key: "oled", label: "纯黑" },
  { key: "contrast", label: "高对比" },
  { key: "pixel", label: "像素" },
  { key: "toon", label: "卡通" },
];

export const THEME_MODES: ReadonlyArray<{
  key: ThemeMode;
  label: string;
}> = [
  { key: "light", label: "浅色" },
  { key: "dark", label: "深色" },
  { key: "system", label: "跟随系统" },
];

const presetKeys = new Set<ThemePreset>(THEME_PRESETS.map(({ key }) => key));
const modeKeys = new Set<ThemeMode>(THEME_MODES.map(({ key }) => key));

export function resolveThemeAppearance(
  storedPreset: unknown,
  storedMode: unknown,
  prefersDark: boolean,
): ThemeAppearance {
  const preset = presetKeys.has(storedPreset as ThemePreset)
    ? (storedPreset as ThemePreset)
    : "paper";
  const mode = modeKeys.has(storedMode as ThemeMode)
    ? (storedMode as ThemeMode)
    : "system";
  return {
    preset,
    mode,
    dark: mode === "dark" || (mode === "system" && prefersDark),
  };
}
