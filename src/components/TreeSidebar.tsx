"use client";

import { ChevronRight, FileText, Folder } from "lucide-react";
import type { PublishedEntry } from "@/lib/vault";
import { entryDirectoryPath } from "@/lib/document-tree";

type FolderNode = {
  folders: Map<string, FolderNode>;
  notes: PublishedEntry[];
};

function newFolder(): FolderNode {
  return { folders: new Map(), notes: [] };
}

function buildTree(entries: PublishedEntry[]): FolderNode {
  const root = newFolder();
  for (const entry of entries) {
    let node = root;
    for (const seg of entryDirectoryPath(entry)) {
      let next = node.folders.get(seg);
      if (!next) {
        next = newFolder();
        node.folders.set(seg, next);
      }
      node = next;
    }
    node.notes.push(entry);
  }
  return root;
}

type TreeSidebarProps = {
  entries: PublishedEntry[];
  selectedPath: string[];
  onSelectPath: (path: string[]) => void;
};

export function TreeSidebar({
  entries,
  selectedPath,
  onSelectPath,
}: TreeSidebarProps) {
  if (entries.length === 0) return null;

  let node = buildTree(entries);
  for (const seg of selectedPath) {
    const next = node.folders.get(seg);
    if (!next) {
      node = newFolder();
      break;
    }
    node = next;
  }

  const folderNames = [...node.folders.keys()].sort((a, b) =>
    a.localeCompare(b, "zh"),
  );
  const notes = [...node.notes].sort((a, b) => b.publishedAt - a.publishedAt);
  const crumbs = ["全部", ...selectedPath];

  function go(slug: string) {
    const encoded = slug
      .split("/")
      .map((s) => encodeURIComponent(s))
      .join("/");
    window.location.href = `/mdhub/view/${encoded}`;
  }

  return (
    <nav>
      <div className="flex items-center gap-1 text-xs text-stone-400 mb-2 overflow-x-auto whitespace-nowrap">
        {crumbs.map((name, i) => {
          const isCurrent = i === crumbs.length - 1;
          return (
            <span key={i} className="flex items-center gap-1">
              {i > 0 && <ChevronRight size={12} className="text-stone-300" />}
              {isCurrent ? (
                <span className="text-stone-500">{name}</span>
              ) : (
                <button
                  type="button"
                  onClick={() => onSelectPath(selectedPath.slice(0, i))}
                  className="hover:text-stone-700 transition-colors"
                >
                  {name}
                </button>
              )}
            </span>
          );
        })}
      </div>

      <ul className="space-y-0.5">
        {folderNames.map((name) => (
          <li key={`d-${name}`}>
            <button
              type="button"
              onClick={() => onSelectPath([...selectedPath, name])}
              className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-stone-700 font-medium hover:bg-stone-50 transition-colors"
            >
              <Folder size={15} className="shrink-0 text-stone-400" />
              <span className="truncate">{name}</span>
              <ChevronRight size={14} className="ml-auto shrink-0 text-stone-300" />
            </button>
          </li>
        ))}
        {notes.map((entry) => (
          <li key={`n-${entry.slug}`}>
            <button
              type="button"
              onClick={() => go(entry.slug)}
              className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-stone-600 hover:bg-stone-50 transition-colors"
            >
              <FileText size={15} className="shrink-0 text-stone-300" />
              <span className="truncate">{entry.title}</span>
            </button>
          </li>
        ))}
      </ul>
    </nav>
  );
}
