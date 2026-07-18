"use client";

import { useState } from "react";
import { useFont, FONT_PRESETS, type FontPreset } from "@/components/FontProvider";

export function ViewerActions({
  markdown,
  downloadName,
}: {
  markdown: string;
  downloadName: string;
}) {
  const [copied, setCopied] = useState(false);
  const { font, setFont } = useFont();

  async function copyMarkdown() {
    try {
      await navigator.clipboard.writeText(markdown);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      const ta = document.createElement("textarea");
      ta.value = markdown;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand("copy");
      document.body.removeChild(ta);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }
  }

  function downloadMarkdown() {
    const blob = new Blob([markdown], { type: "text/markdown" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = downloadName;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }

  return (
    <div className="flex shrink-0 items-center gap-2">
      <select
        value={font}
        onChange={(e) => setFont(e.target.value as FontPreset)}
        className="rounded-md border border-stone-300 bg-white px-2 py-1.5 text-xs font-medium text-stone-600 hover:bg-stone-50 cursor-pointer appearance-none"
      >
        {FONT_PRESETS.map((p) => (
          <option key={p.key} value={p.key}>
            {p.label}
          </option>
        ))}
      </select>
      <button
        type="button"
        onClick={copyMarkdown}
        className="rounded-md border border-stone-300 bg-white px-3 py-1.5 text-xs font-medium text-stone-700 hover:bg-stone-50"
      >
        {copied ? "Copied!" : "Copy"}
      </button>
      <button
        type="button"
        onClick={downloadMarkdown}
        className="rounded-md border border-stone-300 bg-white px-3 py-1.5 text-xs font-medium text-stone-700 hover:bg-stone-50"
      >
        Download
      </button>
    </div>
  );
}
