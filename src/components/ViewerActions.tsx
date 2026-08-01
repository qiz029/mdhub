"use client";

import { useState } from "react";
import { MarkdownEditor } from "@/components/MarkdownEditor";

export function ViewerActions({
  markdown,
  downloadName,
  slug,
}: {
  markdown: string;
  downloadName: string;
  slug: string;
}) {
  const [copied, setCopied] = useState(false);
  const [editToken, setEditToken] = useState<string | null>(null);

  function beginEditing() {
    const stored = sessionStorage.getItem("mdhub-edit-token") || "";
    const token = stored || window.prompt("输入 MDHub 编辑令牌") || "";
    if (!token) return;
    sessionStorage.setItem("mdhub-edit-token", token);
    setEditToken(token);
  }

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
    <>
      <div className="flex shrink-0 items-center gap-2">
        <button
          type="button"
          onClick={beginEditing}
          className="rounded-md bg-stone-900 px-4 py-2.5 text-sm font-medium text-white hover:opacity-85"
        >
          Edit
        </button>
        <button
          type="button"
          onClick={copyMarkdown}
          className="rounded-md border border-stone-300 bg-white px-4 py-2.5 text-sm font-medium text-stone-700 hover:bg-stone-50"
        >
          {copied ? "Copied!" : "Copy"}
        </button>
        <button
          type="button"
          onClick={downloadMarkdown}
          className="rounded-md border border-stone-300 bg-white px-4 py-2.5 text-sm font-medium text-stone-700 hover:bg-stone-50"
        >
          Download
        </button>
      </div>
      {editToken && (
        <MarkdownEditor
          slug={slug}
          initialMarkdown={markdown}
          editToken={editToken}
          onClose={() => setEditToken(null)}
        />
      )}
    </>
  );
}
