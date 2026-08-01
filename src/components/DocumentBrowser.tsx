"use client";

import { useId, useMemo, useRef, useState, type ReactNode } from "react";
import { ChevronDown, FolderTree } from "lucide-react";
import type { PublishedEntry } from "@/lib/vault";
import { isEntryWithinFolder } from "@/lib/document-tree";
import { TreeSidebar } from "./TreeSidebar";

type DocumentBrowserItem = {
  entry: PublishedEntry;
  card: ReactNode;
};

type DocumentBrowserProps = {
  documents: DocumentBrowserItem[];
};

type MobileFolderPickerProps = {
  entries: PublishedEntry[];
  selectedPath: string[];
  onSelectPath: (path: string[]) => void;
};

function MobileFolderPicker({
  entries,
  selectedPath,
  onSelectPath,
}: MobileFolderPickerProps) {
  const [open, setOpen] = useState(false);
  const panelId = useId();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const label =
    selectedPath.length === 0 ? "全部文档" : selectedPath.join(" / ");

  function selectPath(path: string[]) {
    onSelectPath(path);
    setOpen(false);
    triggerRef.current?.focus();
  }

  if (entries.length === 0) return null;

  return (
    <section className="overflow-hidden rounded-xl border border-stone-200 bg-stone-50 md:hidden">
      <button
        ref={triggerRef}
        type="button"
        aria-expanded={open}
        aria-controls={panelId}
        aria-label={`选择文档文件夹，当前：${label}`}
        onClick={() => setOpen((value) => !value)}
        className="flex min-h-11 w-full items-center gap-2.5 px-3 py-2.5 text-left text-[15px] font-semibold text-stone-700 transition-colors hover:bg-stone-100"
      >
        <FolderTree size={18} className="shrink-0 text-stone-400" />
        <span className="min-w-0 flex-1 truncate">{label}</span>
        <ChevronDown
          size={17}
          className={`shrink-0 text-stone-400 transition-transform ${
            open ? "rotate-180" : ""
          }`}
        />
      </button>
      <div
        id={panelId}
        hidden={!open}
        className="max-h-[60vh] overflow-y-auto border-t border-stone-200 px-2 py-3"
      >
        <TreeSidebar
          entries={entries}
          selectedPath={selectedPath}
          onSelectPath={selectPath}
        />
      </div>
    </section>
  );
}

export function DocumentBrowser({ documents }: DocumentBrowserProps) {
  const [folderPath, setFolderPath] = useState<string[]>([]);
  const entries = useMemo(
    () => documents.map(({ entry }) => entry),
    [documents],
  );
  const visibleDocuments = useMemo(
    () =>
      documents.filter(({ entry }) =>
        isEntryWithinFolder(entry, folderPath),
      ),
    [documents, folderPath],
  );

  const description =
    folderPath.length === 0
      ? "来自家庭知识库的笔记与报告"
      : `${folderPath.join(" / ")}下的所有文档`;

  return (
    <div className="flex gap-8">
      <aside className="hidden md:block w-64 shrink-0">
        <div className="sticky top-6 self-start">
          <TreeSidebar
            entries={entries}
            selectedPath={folderPath}
            onSelectPath={setFolderPath}
          />
        </div>
      </aside>
      <div className="min-w-0 flex-1">
        <MobileFolderPicker
          entries={entries}
          selectedPath={folderPath}
          onSelectPath={setFolderPath}
        />
        <div className="mt-6 md:mt-0">
          <h1 className="text-2xl font-bold tracking-tight text-stone-900">
            已发布
          </h1>
          <p className="mt-1 text-sm text-stone-400">{description}</p>
        </div>

        <div className="mt-6 md:mt-8">
          {entries.length === 0 ? (
            <div className="py-24 text-center">
              <p className="text-sm text-stone-400">Nothing published yet.</p>
              <p className="mt-1 text-xs text-stone-300">
                Add <code className="text-stone-400">publish: true</code> to a
                note&apos;s frontmatter to see it here.
              </p>
            </div>
          ) : visibleDocuments.length === 0 ? (
            <div className="py-24 text-center">
              <p className="text-sm text-stone-400">这个文件夹下暂无文档。</p>
            </div>
          ) : (
            <div className="overflow-hidden">
              {visibleDocuments.map(({ card }) => card)}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
