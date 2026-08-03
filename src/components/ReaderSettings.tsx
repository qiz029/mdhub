"use client";

import { useState } from "react";
import {
  useFont,
  FONT_PRESETS,
  FONT_SIZES,
  CONTENT_WIDTHS,
  type FontPreset,
  type FontSize,
  type ContentWidth,
} from "@/components/FontProvider";

function OptionRow<T extends string>({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: { key: T; label: string }[];
  value: T;
  onChange: (v: T) => void;
}) {
  return (
    <div>
      <div className="mb-1.5 text-xs text-stone-400">{label}</div>
      <div className="flex flex-wrap gap-1">
        {options.map((o) => (
          <button
            key={o.key}
            type="button"
            onClick={() => onChange(o.key)}
            className={`rounded-md px-2.5 py-1.5 text-xs ${
              value === o.key
                ? "bg-stone-800 text-white"
                : "bg-stone-100 text-stone-600 hover:bg-stone-200"
            }`}
          >
            {o.label}
          </button>
        ))}
      </div>
    </div>
  );
}

export function ReaderSettings() {
  const [open, setOpen] = useState(false);
  const {
    font,
    setFont,
    fontSize,
    setFontSize,
    contentWidth,
    setContentWidth,
  } = useFont();

  return (
    <>
      {open && (
        <button
          type="button"
          aria-label="关闭"
          className="fixed inset-0 z-40 cursor-default"
          onClick={() => setOpen(false)}
        />
      )}
      <div className="fixed bottom-5 right-5 z-50 flex flex-col items-end gap-2">
        {open && (
          <div className="w-64 space-y-4 rounded-xl border border-stone-200 bg-white p-4 shadow-xl">
            <OptionRow<FontPreset>
              label="字体"
              options={FONT_PRESETS}
              value={font}
              onChange={setFont}
            />
            <OptionRow<FontSize>
              label="字号"
              options={FONT_SIZES}
              value={fontSize}
              onChange={setFontSize}
            />
            <OptionRow<ContentWidth>
              label="页面宽度"
              options={CONTENT_WIDTHS}
              value={contentWidth}
              onChange={setContentWidth}
            />
          </div>
        )}
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          className={`rounded-full px-4 py-2.5 text-sm font-medium shadow-lg ${
            open
              ? "bg-stone-800 text-white"
              : "border border-stone-200 bg-white text-stone-700 hover:bg-stone-50"
          }`}
        >
          Aa
        </button>
      </div>
    </>
  );
}
