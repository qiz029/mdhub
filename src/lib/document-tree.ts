import type { PublishedEntry } from "./vault";

export function entryDirectoryPath(entry: PublishedEntry): string[] {
  if (entry.category) return entry.category.split("/").filter(Boolean);

  const parts = entry.slug.split("/");
  return parts.slice(0, -1).filter(Boolean);
}

export function isEntryWithinFolder(
  entry: PublishedEntry,
  folderPath: string[],
): boolean {
  const entryPath = entryDirectoryPath(entry);
  return folderPath.every((segment, index) => entryPath[index] === segment);
}
