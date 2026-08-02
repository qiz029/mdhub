"use client";

import { ImagePlus, Save, X } from "lucide-react";
import { useRef, useState, type ClipboardEvent, type DragEvent } from "react";
import { useRouter } from "next/navigation";
import {
  imageAltText,
  insertImageMarkdown,
  uploadImage,
} from "@/lib/image-upload";

function documentEndpoint(slug: string): string {
  return (
    "/mdhub/api/document/" +
    slug
      .split("/")
      .map((part) => encodeURIComponent(part))
      .join("/")
  );
}

export function MarkdownEditor({
  slug,
  initialMarkdown,
  onClose,
}: {
  slug: string;
  initialMarkdown: string;
  onClose: () => void;
}) {
  const router = useRouter();
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const selectionRef = useRef({ start: 0, end: 0 });
  const [markdown, setMarkdown] = useState(initialMarkdown);
  const [saving, setSaving] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState("");

  const dirty = markdown !== initialMarkdown;

  function rememberSelection() {
    const textarea = textareaRef.current;
    if (!textarea) return;
    selectionRef.current = {
      start: textarea.selectionStart,
      end: textarea.selectionEnd,
    };
  }

  function close() {
    if (dirty && !window.confirm("放弃尚未保存的修改？")) return;
    onClose();
  }

  async function insertImage(file: File) {
    if (uploading) return;
    setUploading(true);
    setError("");
    try {
      const uploaded = await uploadImage(file);
      const selection = selectionRef.current;
      const insertion = insertImageMarkdown(
        markdown,
        selection.start,
        selection.end,
        uploaded.href,
        imageAltText(file.name),
      );
      setMarkdown(insertion.markdown);
      selectionRef.current = { start: insertion.cursor, end: insertion.cursor };
      requestAnimationFrame(() => {
        textareaRef.current?.focus();
        textareaRef.current?.setSelectionRange(
          insertion.cursor,
          insertion.cursor,
        );
      });
    } catch (uploadError) {
      setError(uploadError instanceof Error ? uploadError.message : "图片上传失败");
    } finally {
      setUploading(false);
      if (fileInputRef.current) fileInputRef.current.value = "";
    }
  }

  async function save() {
    if (saving || !dirty) return;
    setSaving(true);
    setError("");
    try {
      const response = await fetch(documentEndpoint(slug), {
        method: "PUT",
        headers: {
          "Content-Type": "text/markdown; charset=utf-8",
        },
        body: markdown,
      });
      const result = (await response.json().catch(() => ({}))) as {
        error?: string;
      };
      if (!response.ok) {
        throw new Error(result.error || `保存失败（HTTP ${response.status}）`);
      }
      onClose();
      router.refresh();
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "保存失败");
    } finally {
      setSaving(false);
    }
  }

  function onPaste(event: ClipboardEvent<HTMLTextAreaElement>) {
    const image = Array.from(event.clipboardData.files).find((file) =>
      file.type.startsWith("image/"),
    );
    if (!image) return;
    event.preventDefault();
    rememberSelection();
    void insertImage(image);
  }

  function onDrop(event: DragEvent<HTMLTextAreaElement>) {
    const image = Array.from(event.dataTransfer.files).find((file) =>
      file.type.startsWith("image/"),
    );
    if (!image) return;
    event.preventDefault();
    rememberSelection();
    void insertImage(image);
  }

  return (
    <div className="fixed inset-0 z-50 flex bg-stone-900/45 p-3 backdrop-blur-sm sm:p-6">
      <section
        role="dialog"
        aria-modal="true"
        aria-label="编辑 Markdown"
        className="mx-auto flex w-full max-w-5xl flex-col overflow-hidden rounded-xl border border-stone-200 bg-white shadow-2xl"
      >
        <header className="flex flex-wrap items-center gap-2 border-b border-stone-200 px-3 py-3 sm:px-4">
          <div className="mr-auto min-w-0">
            <p className="truncate text-sm font-semibold text-stone-800">
              {slug}
            </p>
            <p className="text-xs text-stone-400">
              支持选择、拖放或粘贴图片；大图会自动优化为 WebP
            </p>
          </div>
          <input
            ref={fileInputRef}
            type="file"
            accept="image/png,image/jpeg,image/gif,image/webp,image/avif"
            className="hidden"
            onChange={(event) => {
              const file = event.target.files?.[0];
              if (file) void insertImage(file);
            }}
          />
          <button
            type="button"
            disabled={uploading}
            onMouseDown={rememberSelection}
            onClick={() => fileInputRef.current?.click()}
            className="inline-flex min-h-10 items-center gap-2 rounded-lg border border-stone-300 px-3 text-sm font-medium text-stone-700 hover:bg-stone-50 disabled:opacity-50"
          >
            <ImagePlus size={16} />
            {uploading ? "处理中…" : "插入图片"}
          </button>
          <button
            type="button"
            disabled={saving || !dirty}
            onClick={() => void save()}
            className="inline-flex min-h-10 items-center gap-2 rounded-lg bg-stone-900 px-3 text-sm font-medium text-white hover:opacity-85 disabled:opacity-40"
          >
            <Save size={16} />
            {saving ? "保存中…" : "保存"}
          </button>
          <button
            type="button"
            aria-label="关闭编辑器"
            onClick={close}
            className="inline-flex min-h-10 min-w-10 items-center justify-center rounded-lg border border-stone-300 text-stone-500 hover:bg-stone-50 hover:text-stone-800"
          >
            <X size={18} />
          </button>
        </header>

        {error && (
          <div className="border-b border-red-200 bg-red-50 px-4 py-2 text-sm text-red-600">
            {error}
          </div>
        )}

        <textarea
          ref={textareaRef}
          value={markdown}
          autoFocus
          spellCheck={false}
          readOnly={uploading || saving}
          onChange={(event) => setMarkdown(event.target.value)}
          onSelect={rememberSelection}
          onKeyUp={rememberSelection}
          onClick={rememberSelection}
          onPaste={onPaste}
          onDragOver={(event) => {
            if (
              Array.from(event.dataTransfer.items).some((item) =>
                item.type.startsWith("image/"),
              )
            ) {
              event.preventDefault();
            }
          }}
          onDrop={onDrop}
          className="min-h-0 flex-1 resize-none bg-stone-50 p-4 font-mono text-sm leading-6 text-stone-800 outline-none read-only:opacity-70 sm:p-5"
        />
      </section>
    </div>
  );
}
