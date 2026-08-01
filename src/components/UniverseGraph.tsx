"use client";

import Link from "next/link";
import {
  forceCenter,
  forceCollide,
  forceLink,
  forceManyBody,
  forceSimulation,
  type Simulation,
  type SimulationLinkDatum,
  type SimulationNodeDatum,
} from "d3-force";
import { ExternalLink, LocateFixed, Search } from "lucide-react";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
  type WheelEvent as ReactWheelEvent,
} from "react";
import type { UniverseEdge, UniverseGraph, UniverseNode } from "@/lib/universe";

type LayoutNode = UniverseNode & SimulationNodeDatum;
type LayoutEdge = UniverseEdge & SimulationLinkDatum<LayoutNode>;
type ViewTransform = { x: number; y: number; k: number };
type Gesture =
  | { kind: "pan"; x: number; y: number; originX: number; originY: number }
  | { kind: "node"; node: LayoutNode };

const clusterColors = [
  "#c15f3c",
  "#55766a",
  "#6e6a9b",
  "#b4843f",
  "#4e7596",
  "#9a5e67",
  "#64824d",
  "#8b6b47",
];

function hashString(value: string): number {
  let hash = 2166136261;
  for (let i = 0; i < value.length; i++) {
    hash ^= value.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return hash >>> 0;
}

function nodeColor(node: UniverseNode): string {
  const key = node.category || node.tags[0] || "uncategorized";
  return clusterColors[hashString(key) % clusterColors.length];
}

function nodeRadius(node: UniverseNode): number {
  return 5 + Math.min(8, Math.sqrt(node.degree) * 2.2);
}

function endpointNode(endpoint: string | number | LayoutNode): LayoutNode | null {
  return typeof endpoint === "object" ? endpoint : null;
}

function viewHref(slug: string): string {
  return (
    "/view/" +
    slug
      .split("/")
      .map((part) => encodeURIComponent(part))
      .join("/")
  );
}

function browserViewHref(slug: string): string {
  return `/mdhub${viewHref(slug)}`;
}

export function UniverseGraphView({ graph }: { graph: UniverseGraph }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const hostRef = useRef<HTMLDivElement>(null);
  const nodesRef = useRef<LayoutNode[]>([]);
  const edgesRef = useRef<LayoutEdge[]>([]);
  const simulationRef = useRef<Simulation<LayoutNode, LayoutEdge> | null>(null);
  const transformRef = useRef<ViewTransform>({ x: 0, y: 0, k: 1 });
  const gestureRef = useRef<Gesture | null>(null);
  const selectedRef = useRef<string | null>(null);
  const hoveredRef = useRef<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [hoveredId, setHoveredId] = useState<string | null>(null);
  const [density, setDensity] = useState<"focused" | "balanced" | "full">(
    "balanced",
  );
  const [query, setQuery] = useState("");

  const visibleEdges = useMemo(() => {
    if (density === "full" || graph.edges.length < 4) return graph.edges;
    const ratio = density === "focused" ? 0.45 : 0.72;
    const count = Math.max(1, Math.ceil(graph.edges.length * ratio));
    return [...graph.edges]
      .sort((a, b) => b.similarity - a.similarity)
      .slice(0, count);
  }, [density, graph.edges]);

  const selected = useMemo(
    () => graph.nodes.find((node) => node.id === selectedId) ?? null,
    [graph.nodes, selectedId],
  );

  const related = useMemo(() => {
    if (!selectedId) return [];
    const nodes = new Map(graph.nodes.map((node) => [node.id, node]));
    return graph.edges
      .flatMap((edge) => {
        if (edge.source === selectedId) {
          return [{ node: nodes.get(edge.target), similarity: edge.similarity }];
        }
        if (edge.target === selectedId) {
          return [{ node: nodes.get(edge.source), similarity: edge.similarity }];
        }
        return [];
      })
      .filter((item): item is { node: UniverseNode; similarity: number } =>
        Boolean(item.node),
      )
      .sort((a, b) => b.similarity - a.similarity);
  }, [graph.edges, graph.nodes, selectedId]);

  const searchResults = useMemo(() => {
    const value = query.trim().toLocaleLowerCase();
    if (!value) return [];
    return graph.nodes
      .filter(
        (node) =>
          node.title.toLocaleLowerCase().includes(value) ||
          node.tags.some((tag) => tag.toLocaleLowerCase().includes(value)),
      )
      .slice(0, 8);
  }, [graph.nodes, query]);

  useEffect(() => {
    selectedRef.current = selectedId;
  }, [selectedId]);

  useEffect(() => {
    hoveredRef.current = hoveredId;
  }, [hoveredId]);

  const render = useCallback(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const context = canvas.getContext("2d");
    if (!context) return;
    const dpr = window.devicePixelRatio || 1;
    const width = canvas.width / dpr;
    const height = canvas.height / dpr;
    const style = getComputedStyle(document.documentElement);
    const background = style.getPropertyValue("--background").trim() || "#faf9f6";
    const foreground = style.getPropertyValue("--foreground").trim() || "#211f1c";
    const border = style.getPropertyValue("--border-subtle").trim() || "#e7e4da";
    const transform = transformRef.current;

    context.setTransform(dpr, 0, 0, dpr, 0, 0);
    context.clearRect(0, 0, width, height);
    context.fillStyle = background;
    context.fillRect(0, 0, width, height);
    context.save();
    context.translate(transform.x, transform.y);
    context.scale(transform.k, transform.k);

    const selectedValue = selectedRef.current;
    const hoveredValue = hoveredRef.current;
    for (const edge of edgesRef.current) {
      const source = endpointNode(edge.source);
      const target = endpointNode(edge.target);
      if (!source || !target || source.x == null || source.y == null || target.x == null || target.y == null) {
        continue;
      }
      const emphasized =
        selectedValue === source.id ||
        selectedValue === target.id ||
        hoveredValue === source.id ||
        hoveredValue === target.id;
      const strength = Math.max(0, Math.min(1, edge.similarity));
      context.beginPath();
      context.moveTo(source.x, source.y);
      context.lineTo(target.x, target.y);
      context.strokeStyle = emphasized ? nodeColor(source) : border;
      context.globalAlpha = emphasized ? 0.9 : 0.2 + strength * 0.32;
      context.lineWidth = (emphasized ? 2.2 : 0.6 + strength) / transform.k;
      context.stroke();
    }
    context.globalAlpha = 1;

    for (const node of nodesRef.current) {
      if (node.x == null || node.y == null) continue;
      const radius = nodeRadius(node);
      const active = node.id === selectedValue || node.id === hoveredValue;
      context.beginPath();
      context.arc(node.x, node.y, radius + (active ? 3 : 0), 0, Math.PI * 2);
      context.fillStyle = active ? foreground : background;
      context.globalAlpha = active ? 0.16 : 0.92;
      context.fill();
      context.beginPath();
      context.arc(node.x, node.y, radius, 0, Math.PI * 2);
      context.fillStyle = node.embedded ? nodeColor(node) : border;
      context.globalAlpha = node.embedded ? 0.95 : 0.72;
      context.fill();
      context.strokeStyle = background;
      context.lineWidth = 1.5 / transform.k;
      context.stroke();

      const showLabel =
        active ||
        (transform.k > 1.45 && node.degree >= 4) ||
        (transform.k > 2.2 && node.degree >= 2);
      if (showLabel) {
        context.globalAlpha = 1;
        context.fillStyle = foreground;
        context.font = `${active ? 600 : 500} ${12 / transform.k}px ui-sans-serif, system-ui`;
        context.textAlign = "center";
        context.fillText(node.title, node.x, node.y + radius + 16 / transform.k);
      }
    }
    context.globalAlpha = 1;
    context.restore();
  }, []);

  useEffect(() => {
    const host = hostRef.current;
    const canvas = canvasRef.current;
    if (!host || !canvas) return;

    const width = Math.max(320, host.clientWidth);
    const height = Math.max(480, host.clientHeight);
    const centerX = width / 2;
    const centerY = height / 2;
    const nodes: LayoutNode[] = graph.nodes.map((node, index) => {
      const seed = hashString(node.id);
      const angle = ((seed % 360) * Math.PI) / 180;
      const ring = 50 + (index % 9) * 18;
      return {
        ...node,
        x: centerX + Math.cos(angle) * ring,
        y: centerY + Math.sin(angle) * ring,
      };
    });
    const edges: LayoutEdge[] = visibleEdges.map((edge) => ({ ...edge }));
    nodesRef.current = nodes;
    edgesRef.current = edges;

    const simulation = forceSimulation<LayoutNode>(nodes)
      .force(
        "link",
        forceLink<LayoutNode, LayoutEdge>(edges)
          .id((node) => node.id)
          .distance((edge) => 65 + (1 - edge.similarity) * 260)
          .strength((edge) => 0.18 + Math.max(0, edge.similarity) * 0.55),
      )
      .force("charge", forceManyBody<LayoutNode>().strength(-160).distanceMax(520))
      .force("center", forceCenter(centerX, centerY).strength(0.06))
      .force("collision", forceCollide<LayoutNode>().radius((node) => nodeRadius(node) + 10))
      .alphaDecay(0.028)
      .on("tick", render);
    simulationRef.current = simulation;

    function resize() {
      const dpr = window.devicePixelRatio || 1;
      const nextWidth = Math.max(320, host!.clientWidth);
      const nextHeight = Math.max(480, host!.clientHeight);
      canvas!.width = Math.round(nextWidth * dpr);
      canvas!.height = Math.round(nextHeight * dpr);
      canvas!.style.width = `${nextWidth}px`;
      canvas!.style.height = `${nextHeight}px`;
      simulation.force("center", forceCenter(nextWidth / 2, nextHeight / 2).strength(0.06));
      simulation.alpha(0.28).restart();
      render();
    }
    const observer = new ResizeObserver(resize);
    observer.observe(host);
    resize();

    const themeObserver = new MutationObserver(render);
    themeObserver.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class", "style"],
    });
    return () => {
      observer.disconnect();
      themeObserver.disconnect();
      simulation.stop();
      if (simulationRef.current === simulation) simulationRef.current = null;
    };
  }, [graph.nodes, render, visibleEdges]);

  function canvasPoint(event: { clientX: number; clientY: number }) {
    const rect = canvasRef.current!.getBoundingClientRect();
    return { x: event.clientX - rect.left, y: event.clientY - rect.top };
  }

  function worldPoint(event: { clientX: number; clientY: number }) {
    const point = canvasPoint(event);
    const transform = transformRef.current;
    return {
      x: (point.x - transform.x) / transform.k,
      y: (point.y - transform.y) / transform.k,
    };
  }

  function hitNode(event: { clientX: number; clientY: number }): LayoutNode | null {
    const point = worldPoint(event);
    const scale = transformRef.current.k;
    for (let i = nodesRef.current.length - 1; i >= 0; i--) {
      const node = nodesRef.current[i];
      if (node.x == null || node.y == null) continue;
      const dx = point.x - node.x;
      const dy = point.y - node.y;
      const radius = nodeRadius(node) + 7 / scale;
      if (dx * dx + dy * dy <= radius * radius) return node;
    }
    return null;
  }

  function onPointerDown(event: ReactPointerEvent<HTMLCanvasElement>) {
    event.currentTarget.setPointerCapture(event.pointerId);
    const node = hitNode(event);
    if (node) {
      const point = worldPoint(event);
      node.fx = point.x;
      node.fy = point.y;
      gestureRef.current = { kind: "node", node };
      setSelectedId(node.id);
      simulationRef.current?.alphaTarget(0.16).restart();
      return;
    }
    const point = canvasPoint(event);
    gestureRef.current = {
      kind: "pan",
      x: point.x,
      y: point.y,
      originX: transformRef.current.x,
      originY: transformRef.current.y,
    };
  }

  function onPointerMove(event: ReactPointerEvent<HTMLCanvasElement>) {
    const gesture = gestureRef.current;
    if (gesture?.kind === "node") {
      const point = worldPoint(event);
      gesture.node.fx = point.x;
      gesture.node.fy = point.y;
      render();
      return;
    }
    if (gesture?.kind === "pan") {
      const point = canvasPoint(event);
      transformRef.current.x = gesture.originX + point.x - gesture.x;
      transformRef.current.y = gesture.originY + point.y - gesture.y;
      render();
      return;
    }
    const node = hitNode(event);
    const next = node?.id ?? null;
    if (next !== hoveredRef.current) {
      hoveredRef.current = next;
      setHoveredId(next);
      event.currentTarget.style.cursor = node ? "pointer" : "grab";
      render();
    }
  }

  function endPointer(event: ReactPointerEvent<HTMLCanvasElement>) {
    if (gestureRef.current?.kind === "node") {
      gestureRef.current.node.fx = null;
      gestureRef.current.node.fy = null;
      simulationRef.current?.alphaTarget(0);
    }
    gestureRef.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
  }

  function onWheel(event: ReactWheelEvent<HTMLCanvasElement>) {
    event.preventDefault();
    const point = canvasPoint(event);
    const transform = transformRef.current;
    const nextScale = Math.max(0.45, Math.min(3.5, transform.k * Math.exp(-event.deltaY * 0.0012)));
    const worldX = (point.x - transform.x) / transform.k;
    const worldY = (point.y - transform.y) / transform.k;
    transform.x = point.x - worldX * nextScale;
    transform.y = point.y - worldY * nextScale;
    transform.k = nextScale;
    render();
  }

  function focusNode(id: string) {
    const node = nodesRef.current.find((item) => item.id === id);
    const canvas = canvasRef.current;
    if (!node || !canvas || node.x == null || node.y == null) return;
    const dpr = window.devicePixelRatio || 1;
    const width = canvas.width / dpr;
    const height = canvas.height / dpr;
    const scale = Math.max(1.25, transformRef.current.k);
    transformRef.current = {
      x: width / 2 - node.x * scale,
      y: height / 2 - node.y * scale,
      k: scale,
    };
    setSelectedId(id);
    setQuery("");
    render();
  }

  function resetView() {
    transformRef.current = { x: 0, y: 0, k: 1 };
    setSelectedId(null);
    render();
  }

  const coverage = graph.meta.documents
    ? Math.round((graph.meta.embedded_documents / graph.meta.documents) * 100)
    : 0;

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
      <div
        ref={hostRef}
        className="relative min-h-[34rem] overflow-hidden rounded-2xl border border-stone-200 bg-stone-50 lg:h-[calc(100vh-13rem)] lg:min-h-[38rem]"
      >
        <canvas
          ref={canvasRef}
          aria-label={`Knowledge Universe：${graph.meta.documents} 篇文档，${graph.meta.edges} 条语义关系`}
          className="block touch-none"
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={endPointer}
          onPointerCancel={endPointer}
          onPointerLeave={() => {
            if (!gestureRef.current) {
              hoveredRef.current = null;
              setHoveredId(null);
              render();
            }
          }}
          onDoubleClick={(event) => {
            const node = hitNode(event);
            if (node) window.location.href = browserViewHref(node.id);
          }}
          onWheel={onWheel}
        />

        <div className="pointer-events-none absolute left-3 top-3 flex flex-wrap gap-2 text-xs sm:left-4 sm:top-4">
          <span className="rounded-full border border-stone-200 bg-white/90 px-2.5 py-1.5 text-stone-600 backdrop-blur">
            {graph.meta.documents} nodes
          </span>
          <span className="rounded-full border border-stone-200 bg-white/90 px-2.5 py-1.5 text-stone-600 backdrop-blur">
            {visibleEdges.length} edges
          </span>
          <span className="hidden rounded-full border border-stone-200 bg-white/90 px-2.5 py-1.5 text-stone-600 backdrop-blur sm:inline-flex">
            {coverage}% embedded
          </span>
        </div>

        <button
          type="button"
          aria-label="重置视图"
          onClick={resetView}
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
          拖动画布 · 滚轮缩放 · 双击打开文档
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
              onChange={(event) =>
                setDensity(event.target.value as "focused" | "balanced" | "full")
              }
              className="mt-2 block w-full rounded-lg border border-stone-200 bg-stone-50 px-3 py-2.5 text-sm font-medium normal-case tracking-normal text-stone-700"
            >
              <option value="focused">Focused · strongest 45%</option>
              <option value="balanced">Balanced · strongest 72%</option>
              <option value="full">Full · all relationships</option>
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
              {selected.category && (
                <p className="mt-1.5 text-xs text-stone-400">{selected.category}</p>
              )}
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
                          style={{ backgroundColor: nodeColor(node) }}
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
