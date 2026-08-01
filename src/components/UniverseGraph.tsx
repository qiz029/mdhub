"use client";

import Link from "next/link";
import { ExternalLink, LocateFixed, Search } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import type { UniverseGraph } from "@/lib/universe";
import {
  deriveUniverseProjection,
  type EdgeDensity,
} from "@/lib/universe-layout";
import {
  UniverseCanvas,
  universeNodeColor,
  type UniverseCanvasHandle,
} from "./UniverseCanvas";

function viewHref(slug: string): string {
  return (
    "/view/" +
    slug
      .split("/")
      .map((part) => encodeURIComponent(part))
      .join("/")
  );
}

export function UniverseGraphView({ graph }: { graph: UniverseGraph }) {
  const canvasRef = useRef<UniverseCanvasHandle>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [density, setDensity] = useState<EdgeDensity>("balanced");
  const [query, setQuery] = useState("");
  const {
    localEdges,
    clusterEdges,
    layoutKey,
    renderedEdges,
    selected,
    related,
    searchResults,
    coverage,
    clusterCount,
  } = useMemo(
    () => deriveUniverseProjection(graph, density, selectedId, query),
    [density, graph, query, selectedId],
  );

  function focusNode(id: string) {
    canvasRef.current?.focusNode(id);
    setQuery("");
  }

  if (graph.nodes.length === 0) {
    return (
      <section className="rounded-2xl border border-stone-200 bg-stone-50 px-6 py-20 text-center">
        <p className="font-medium text-stone-700">知识宇宙还是空的</p>
        <p className="mt-2 text-sm text-stone-400">发布文档后，它们会出现在这里。</p>
      </section>
    );
  }

  return (
    <section className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_19rem]">
      <div className="relative min-h-[34rem] overflow-hidden rounded-2xl border border-stone-200 bg-stone-50 lg:h-[calc(100vh-13rem)] lg:min-h-[38rem]">
        <UniverseCanvas
          ref={canvasRef}
          nodes={graph.nodes}
          localEdges={localEdges}
          renderedEdges={renderedEdges}
          clusterEdges={clusterEdges}
          layoutKey={layoutKey}
          selectedId={selectedId}
          onSelect={setSelectedId}
          ariaLabel={`Knowledge Universe：${graph.meta.documents} 篇文档，${clusterCount} 个语义 cluster，${graph.meta.edges} 条语义关系`}
        />

        <div className="pointer-events-none absolute left-3 top-3 flex flex-wrap gap-2 text-xs sm:left-4 sm:top-4">
          <span className="rounded-full border border-stone-200 bg-white/90 px-2.5 py-1.5 text-stone-600 backdrop-blur">
            {graph.meta.documents} nodes
          </span>
          <span className="rounded-full border border-stone-200 bg-white/90 px-2.5 py-1.5 text-stone-600 backdrop-blur">
            {clusterCount} clusters
          </span>
          <span className="rounded-full border border-stone-200 bg-white/90 px-2.5 py-1.5 text-stone-600 backdrop-blur">
            {localEdges.length} local edges
          </span>
          <span className="rounded-full border border-stone-200 bg-white/90 px-2.5 py-1.5 text-stone-600 backdrop-blur">
            {clusterEdges.length} cluster links
          </span>
          <span className="hidden rounded-full border border-stone-200 bg-white/90 px-2.5 py-1.5 text-stone-600 backdrop-blur sm:inline-flex">
            {coverage}% embedded
          </span>
        </div>

        <button
          type="button"
          aria-label="重置视图"
          onClick={() => canvasRef.current?.resetView()}
          className="absolute right-3 top-3 inline-flex min-h-10 min-w-10 items-center justify-center gap-2 rounded-lg border border-stone-200 bg-white/90 px-2.5 text-xs font-medium text-stone-600 backdrop-blur transition-colors hover:bg-white hover:text-stone-900 sm:right-4 sm:top-4 sm:px-3"
        >
          <LocateFixed size={15} />
          <span className="hidden sm:inline">重置视图</span>
        </button>

        {graph.meta.embedded_documents === 0 && (
          <div className="absolute inset-x-4 bottom-4 rounded-xl border border-amber-400/30 bg-amber-100/90 px-4 py-3 text-sm text-amber-700 backdrop-blur">
            还没有可用的 embedding。配置 MDHUB_EMBED_URL 后运行 POST /api/reembed。
          </div>
        )}
        <p className="pointer-events-none absolute bottom-4 left-4 hidden text-xs text-stone-400 sm:block">
          拖动空白处平移 · 滚轮缩放 · 单击选择 · 双击打开
        </p>
      </div>

      <aside className="rounded-2xl border border-stone-200 bg-white p-4 lg:h-[calc(100vh-13rem)] lg:min-h-[38rem] lg:overflow-y-auto">
        <label className="block">
          <span className="text-xs font-semibold uppercase tracking-widest text-stone-400">
            Find a document
          </span>
          <span className="relative mt-2 block">
            <Search
              size={15}
              className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-stone-400"
            />
            <input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="标题或标签"
              className="w-full rounded-lg border border-stone-200 bg-stone-50 py-2.5 pl-9 pr-3 text-sm text-stone-800 placeholder:text-stone-400 focus:bg-white"
            />
          </span>
        </label>
        {searchResults.length > 0 && (
          <div className="mt-2 overflow-hidden rounded-lg border border-stone-200">
            {searchResults.map((node) => (
              <button
                key={node.id}
                type="button"
                onClick={() => focusNode(node.id)}
                className="block w-full border-b border-stone-100 px-3 py-2 text-left text-sm text-stone-600 transition-colors last:border-0 hover:bg-stone-50 hover:text-stone-900"
              >
                <span className="block truncate font-medium">{node.title}</span>
              </button>
            ))}
          </div>
        )}

        <div className="mt-5 border-t border-stone-100 pt-5">
          <label className="text-xs font-semibold uppercase tracking-widest text-stone-400">
            Edge density
            <select
              value={density}
              onChange={(event) => setDensity(event.target.value as EdgeDensity)}
              className="mt-2 block w-full rounded-lg border border-stone-200 bg-stone-50 px-3 py-2.5 text-sm font-medium normal-case tracking-normal text-stone-700"
            >
              <option value="focused">Focused · Top 2 per node</option>
              <option value="balanced">Balanced · Top 4 per node</option>
              <option value="full">Full · Top 6 per node</option>
            </select>
          </label>
        </div>

        <div className="mt-5 border-t border-stone-100 pt-5">
          {selected ? (
            <>
              <p className="text-xs font-semibold uppercase tracking-widest text-stone-400">
                Selected document
              </p>
              <h2 className="mt-2 text-lg font-semibold leading-snug text-stone-900">
                {selected.title}
              </h2>
              <p className="mt-1.5 text-xs text-stone-400">
                {selected.category && `${selected.category} · `}
                {selected.word_count.toLocaleString()} 字符
              </p>
              {selected.excerpt && (
                <p className="mt-3 line-clamp-4 text-sm leading-6 text-stone-500">
                  {selected.excerpt}
                </p>
              )}
              {selected.tags.length > 0 && (
                <div className="mt-3 flex flex-wrap gap-1.5">
                  {selected.tags.map((tag) => (
                    <span
                      key={tag}
                      className="rounded-full bg-stone-100 px-2 py-1 text-xs text-stone-500"
                    >
                      #{tag}
                    </span>
                  ))}
                </div>
              )}
              <Link
                href={viewHref(selected.id)}
                className="mt-4 inline-flex min-h-10 items-center gap-2 rounded-lg bg-stone-900 px-3.5 text-sm font-medium text-white transition-opacity hover:opacity-85"
              >
                打开文档 <ExternalLink size={14} />
              </Link>

              <div className="mt-5 border-t border-stone-100 pt-4">
                <p className="text-xs font-semibold uppercase tracking-widest text-stone-400">
                  Semantic neighbours
                </p>
                {related.length > 0 ? (
                  <div className="mt-2 space-y-1">
                    {related.map(({ node, similarity }) => (
                      <button
                        key={node.id}
                        type="button"
                        onClick={() => focusNode(node.id)}
                        className="flex w-full items-center gap-3 rounded-lg px-2 py-2 text-left transition-colors hover:bg-stone-50"
                      >
                        <span
                          className="h-2.5 w-2.5 shrink-0 rounded-full"
                          style={{ backgroundColor: universeNodeColor(node) }}
                        />
                        <span className="min-w-0 flex-1 truncate text-sm text-stone-600">
                          {node.title}
                        </span>
                        <span className="text-xs tabular-nums text-stone-400">
                          {similarity.toFixed(2)}
                        </span>
                      </button>
                    ))}
                  </div>
                ) : (
                  <p className="mt-2 text-sm text-stone-400">暂无语义关系。</p>
                )}
              </div>
            </>
          ) : (
            <div className="py-8 text-center">
              <div className="mx-auto h-3 w-3 rounded-full bg-[var(--accent)] opacity-80" />
              <p className="mt-3 text-sm font-medium text-stone-600">选择一个 node</p>
              <p className="mt-1 text-xs leading-5 text-stone-400">
                查看文档摘要、标签和最相似的邻居。
              </p>
            </div>
          )}
        </div>
      </aside>
    </section>
  );
}
