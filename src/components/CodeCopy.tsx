"use client";

import { useEffect } from "react";

// Adds a small copy button to every code block in the article. The button is
// appended to the end of the <pre>, so the block's textContent prefix (used
// for comment anchoring) is unaffected.
export function CodeCopy() {
  useEffect(() => {
    const pres = Array.from(
      document.querySelectorAll<HTMLElement>(".prose-md pre"),
    );
    const cleanups: (() => void)[] = [];

    for (const pre of pres) {
      pre.style.position = "relative";
      const btn = document.createElement("button");
      btn.type = "button";
      btn.textContent = "Copy";
      btn.className =
        "absolute right-2 top-2 rounded-md border border-stone-300 bg-white px-2 py-1 text-xs font-medium text-stone-700 hover:bg-stone-50";
      btn.addEventListener("click", async () => {
        const code = pre.querySelector("code");
        const text = code ? code.textContent || "" : pre.textContent || "";
        try {
          await navigator.clipboard.writeText(text);
        } catch {
          const ta = document.createElement("textarea");
          ta.value = text;
          document.body.appendChild(ta);
          ta.select();
          document.execCommand("copy");
          document.body.removeChild(ta);
        }
        btn.textContent = "Copied!";
        setTimeout(() => {
          btn.textContent = "Copy";
        }, 1500);
      });
      pre.appendChild(btn);
      cleanups.push(() => btn.remove());
    }

    return () => cleanups.forEach((fn) => fn());
  }, []);

  return null;
}
