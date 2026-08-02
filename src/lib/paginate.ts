// Client-side pagination helper. Lists on the Sparks page are already fully
// loaded in the browser (a personal library does not need server-side
// paging); this pure function owns the slicing and clamping so it can be
// unit-tested. Pages are 1-based; out-of-range pages clamp into [1,
// pageCount], which also settles the "items shrank after curation" case back
// onto the last page.

export type Page<T> = {
  pageItems: T[];
  pageCount: number;
  page: number; // clamped, 1-based
};

export function paginate<T>(
  items: readonly T[],
  page: number,
  pageSize: number,
): Page<T> {
  const size = Math.max(1, Math.floor(pageSize) || 1);
  const pageCount = Math.max(1, Math.ceil(items.length / size));
  const clamped = Math.min(Math.max(1, Math.floor(page) || 1), pageCount);
  return {
    pageItems: items.slice((clamped - 1) * size, clamped * size),
    pageCount,
    page: clamped,
  };
}
