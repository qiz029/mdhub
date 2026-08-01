"use client";

import { useMemo, useState, type ReactNode } from "react";
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
      : `${folderPath.join(" / ")} 下的所有文档`;

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
      <div className="min-w-0 flex-1 space-y-8">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-stone-900">
            已发布
          </h1>
          <p className="mt-1 text-sm text-stone-400">{description}</p>
        </div>

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
  );
}
