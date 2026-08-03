"use client";

import {
  forceCenter,
  forceCollide,
  forceLink,
  forceManyBody,
  forceSimulation,
  forceX,
  forceY,
  type SimulationLinkDatum,
  type SimulationNodeDatum,
} from "d3-force";
import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  type PointerEvent as ReactPointerEvent,
  type WheelEvent as ReactWheelEvent,
} from "react";
import type { UniverseEdge, UniverseNode } from "@/lib/universe";
import {
  fitUniverseTransform,
  type ClusterEdge,
  type UniverseViewTransform,
} from "@/lib/universe-layout";

type LayoutNode = UniverseNode & SimulationNodeDatum;
type LayoutEdge = Omit<UniverseEdge, "source" | "target"> &
  SimulationLinkDatum<LayoutNode>;
type Gesture = { x: number; y: number; originX: number; originY: number };
type ClusterCenter = { x: number; y: number };
type ClusterLayoutNode = SimulationNodeDatum & { id: number; size: number };
type ClusterLayoutEdge = ClusterEdge & SimulationLinkDatum<ClusterLayoutNode>;
type LayoutCenters = {
  nodes: Map<string, ClusterCenter>;
  clusters: Map<number, ClusterCenter>;
};

export type UniverseCanvasHandle = {
  focusNode(id: string): void;
  resetView(): void;
};

type UniverseCanvasProps = {
  nodes: UniverseNode[];
  localEdges: UniverseEdge[];
  renderedEdges: UniverseEdge[];
  clusterEdges: ClusterEdge[];
  layoutKey: string;
  selectedId: string | null;
  ariaLabel: string;
  onSelect(id: string | null): void;
};

// Signal pulses ease in: they linger near the source as if charging, then
// fire across the edge. Position uses progress^3; brightness ramps with the
// velocity (~progress^2) so the slow phase reads as a dim charge gathering
// at the node rather than a static dot.
function easeInCubic(t: number): number {
  return t * t * t;
}

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

export function universeNodeColor(node: UniverseNode): string {
  if (node.cluster < 0) return "#aaa49a";
  return clusterColors[node.cluster % clusterColors.length];
}

function clusterColor(cluster: number): string {
  if (cluster < 0) return "#aaa49a";
  return clusterColors[cluster % clusterColors.length];
}

function nodeRadius(node: UniverseNode): number {
  return 5 + Math.min(8, Math.sqrt(Math.max(0, node.word_count) / 120));
}

function endpointNode(endpoint: string | number | LayoutNode): LayoutNode | null {
  return typeof endpoint === "object" ? endpoint : null;
}

function resolveLayoutEdges(
  edges: UniverseEdge[],
  nodes: LayoutNode[],
): LayoutEdge[] {
  const byId = new Map(nodes.map((node) => [node.id, node]));
  return edges.flatMap((edge) => {
    const source = byId.get(edge.source);
    const target = byId.get(edge.target);
    return source && target ? [{ ...edge, source, target }] : [];
  });
}

function browserViewHref(slug: string): string {
  return (
    "/mdhub/view/" +
    slug
      .split("/")
      .map((part) => encodeURIComponent(part))
      .join("/")
  );
}

function layoutCenters(
  nodes: UniverseNode[],
  edges: ClusterEdge[],
  width: number,
  height: number,
): LayoutCenters {
  const counts = new Map<number, number>();
  for (const node of nodes) {
    if (node.cluster < 0) continue;
    counts.set(node.cluster, (counts.get(node.cluster) ?? 0) + 1);
  }
  const clusters = [...counts].sort(
    (left, right) => right[1] - left[1] || left[0] - right[0],
  );
  const clusterPositions = new Map<number, ClusterCenter>();
  const clusterNodes: ClusterLayoutNode[] = clusters.map(
    ([cluster, size], index) => {
      const angle = -Math.PI / 2 + (index * Math.PI * 2) / clusters.length;
      const radiusX = Math.min(width * 0.32, 120 + clusters.length * 20);
      const radiusY = Math.min(height * 0.3, 100 + clusters.length * 18);
      return {
        id: cluster,
        size,
        x: width / 2 + Math.cos(angle) * radiusX,
        y: height / 2 + Math.sin(angle) * radiusY,
      };
    },
  );
  if (clusterNodes.length > 0) {
    const clusterLinks: ClusterLayoutEdge[] = edges.map((edge) => ({
      ...edge,
      source: edge.sourceCluster,
      target: edge.targetCluster,
    }));
    const simulation = forceSimulation<ClusterLayoutNode>(clusterNodes)
      .force(
        "link",
        forceLink<ClusterLayoutNode, ClusterLayoutEdge>(clusterLinks)
          .id((node) => node.id)
          .distance((edge) => 105 + (1 - edge.similarity) * 105)
          .strength((edge) => Math.min(0.72, 0.14 + Math.log1p(edge.count) * 0.13)),
      )
      .force(
        "charge",
        forceManyBody<ClusterLayoutNode>().strength(
          (node) => -230 - node.size * 9,
        ),
      )
      .force("center", forceCenter(width / 2, height / 2).strength(0.08))
      .force(
        "collision",
        forceCollide<ClusterLayoutNode>().radius(
          (node) => 48 + Math.sqrt(node.size) * 8,
        ),
      )
      .stop();
    for (let tick = 0; tick < 180; tick++) simulation.tick();
    const marginX = Math.min(96, width * 0.18);
    const marginY = Math.min(88, height * 0.16);
    for (const node of clusterNodes) {
      clusterPositions.set(node.id, {
        x: Math.max(marginX, Math.min(width - marginX, node.x ?? width / 2)),
        y: Math.max(marginY, Math.min(height - marginY, node.y ?? height / 2)),
      });
    }
  }

  const centers = new Map<string, ClusterCenter>();
  for (const node of nodes) {
    const position = clusterPositions.get(node.cluster);
    if (position) centers.set(node.id, position);
  }
  const unclustered = nodes
    .filter((node) => node.cluster < 0)
    .sort((left, right) => left.id.localeCompare(right.id));
  if (unclustered.length === 1 && clusters.length === 0) {
    centers.set(unclustered[0].id, { x: width / 2, y: height / 2 });
    return { nodes: centers, clusters: clusterPositions };
  }
  unclustered.forEach((node, index) => {
    const angle = -Math.PI / 2 + ((index + 0.5) * Math.PI * 2) / unclustered.length;
    centers.set(node.id, {
      x: width / 2 + Math.cos(angle) * width * 0.43,
      y: height / 2 + Math.sin(angle) * height * 0.4,
    });
  });
  return { nodes: centers, clusters: clusterPositions };
}

export const UniverseCanvas = forwardRef<
  UniverseCanvasHandle,
  UniverseCanvasProps
>(function UniverseCanvas(
  {
    nodes: graphNodes,
    localEdges,
    renderedEdges,
    clusterEdges,
    layoutKey,
    selectedId,
    ariaLabel,
    onSelect,
  },
  ref,
) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const nodesRef = useRef<LayoutNode[]>([]);
  const edgesRef = useRef<LayoutEdge[]>([]);
  const clusterEdgesRef = useRef<ClusterEdge[]>([]);
  const clusterCentersRef = useRef<Map<number, ClusterCenter>>(new Map());
  const transformRef = useRef<UniverseViewTransform>({ x: 0, y: 0, k: 1 });
  const viewTouchedRef = useRef(false);
  const gestureRef = useRef<Gesture | null>(null);
  const selectedRef = useRef<string | null>(selectedId);
  const hoveredRef = useRef<string | null>(null);

  useEffect(() => {
    selectedRef.current = selectedId;
  }, [selectedId]);

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
    const now = performance.now() / 1000;

    context.setTransform(dpr, 0, 0, dpr, 0, 0);
    context.clearRect(0, 0, width, height);
    context.fillStyle = background;
    context.fillRect(0, 0, width, height);
    context.save();
    context.translate(transform.x, transform.y);
    context.scale(transform.k, transform.k);

    const selectedValue = selectedRef.current;
    const hoveredValue = hoveredRef.current;
    for (const edge of clusterEdgesRef.current) {
      const source = clusterCentersRef.current.get(edge.sourceCluster);
      const target = clusterCentersRef.current.get(edge.targetCluster);
      if (!source || !target) continue;
      const dx = target.x - source.x;
      const dy = target.y - source.y;
      const length = Math.max(1, Math.hypot(dx, dy));
      const curve = Math.min(54, length * 0.14);
      const direction =
        (edge.sourceCluster * 31 + edge.targetCluster) % 2 === 0 ? 1 : -1;
      const controlX = (source.x + target.x) / 2 - (dy / length) * curve * direction;
      const controlY = (source.y + target.y) / 2 + (dx / length) * curve * direction;
      context.beginPath();
      context.moveTo(source.x, source.y);
      context.quadraticCurveTo(controlX, controlY, target.x, target.y);
      context.strokeStyle = clusterColor(edge.sourceCluster);
      context.globalAlpha = 0.16 + edge.similarity * 0.24;
      context.lineWidth = (0.8 + Math.log1p(edge.count) * 0.72) / transform.k;
      context.setLineDash([6 / transform.k, 5 / transform.k]);
      context.stroke();

      const clusterSeed = hashString(`${edge.sourceCluster}:${edge.targetCluster}`);
      const clusterRaw = (now * 0.07 + (clusterSeed % 1000) / 1000) % 1;
      const clusterT = easeInCubic(clusterRaw);
      const invT = 1 - clusterT;
      const pulseX =
        invT * invT * source.x +
        2 * invT * clusterT * controlX +
        clusterT * clusterT * target.x;
      const pulseY =
        invT * invT * source.y +
        2 * invT * clusterT * controlY +
        clusterT * clusterT * target.y;
      context.beginPath();
      context.arc(pulseX, pulseY, 2.2 / transform.k, 0, Math.PI * 2);
      context.fillStyle = clusterColor(edge.sourceCluster);
      context.globalAlpha = 0.12 + 0.5 * clusterRaw * clusterRaw;
      context.fill();
    }
    context.setLineDash([]);
    for (const edge of edgesRef.current) {
      const source = endpointNode(edge.source);
      const target = endpointNode(edge.target);
      if (
        !source ||
        !target ||
        source.x == null ||
        source.y == null ||
        target.x == null ||
        target.y == null
      ) {
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
      context.strokeStyle = emphasized ? universeNodeColor(source) : border;
      context.globalAlpha = emphasized ? 0.9 : 0.2 + strength * 0.32;
      context.lineWidth = (emphasized ? 2.2 : 0.6 + strength) / transform.k;
      context.stroke();

      const seed = hashString(`${source.id}→${target.id}`);
      const pulseCount = strength > 0.85 ? 2 : 1;
      const speed = (0.1 + strength * 0.2) * (emphasized ? 1.8 : 1);
      for (let pulse = 0; pulse < pulseCount; pulse++) {
        const raw = (now * speed + (seed % 1000) / 1000 + pulse * 0.5) % 1;
        const eased = easeInCubic(raw);
        const progress = seed % 2 === 0 ? eased : 1 - eased;
        const charge = 0.25 + 0.75 * raw * raw;
        const x = source.x + (target.x - source.x) * progress;
        const y = source.y + (target.y - source.y) * progress;
        const radius = (emphasized ? 2.6 : 1.8) / transform.k;
        context.beginPath();
        context.arc(x, y, radius * 2.6, 0, Math.PI * 2);
        context.fillStyle = emphasized ? universeNodeColor(source) : foreground;
        context.globalAlpha = (emphasized ? 0.16 : 0.08) * charge;
        context.fill();
        context.beginPath();
        context.arc(x, y, radius, 0, Math.PI * 2);
        context.fillStyle = emphasized ? universeNodeColor(source) : foreground;
        context.globalAlpha = (emphasized ? 0.95 : 0.4) * charge;
        context.fill();
      }
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
      context.fillStyle = node.embedded ? universeNodeColor(node) : border;
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

  const fitAllNodes = useCallback(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const dpr = window.devicePixelRatio || 1;
    transformRef.current = fitUniverseTransform(
      nodesRef.current.flatMap((node) =>
        node.x == null || node.y == null
          ? []
          : [{ x: node.x, y: node.y, radius: nodeRadius(node) }],
      ),
      canvas.width / dpr,
      canvas.height / dpr,
    );
    render();
  }, [render]);

  // localEdges and clusterEdges are completely represented by layoutKey;
  // selection/search may allocate equivalent arrays without rebuilding D3.
  useEffect(() => {
    const canvas = canvasRef.current;
    const host = canvas?.parentElement;
    if (!host || !canvas) return;
    // A changed graph or density rebuilds the layout, so stale viewport and
    // pointer state must not be carried into a different coordinate space.
    viewTouchedRef.current = false;
    gestureRef.current = null;
    hoveredRef.current = null;

    const width = Math.max(320, host.clientWidth);
    const height = Math.max(480, host.clientHeight);
    const centerX = width / 2;
    const centerY = height / 2;
    let centers = layoutCenters(graphNodes, clusterEdges, width, height);
    const nodes: LayoutNode[] = graphNodes.map((node, index) => {
      const seed = hashString(node.id);
      const angle = ((seed % 360) * Math.PI) / 180;
      const ring = 22 + (index % 7) * 11;
      const center = centers.nodes.get(node.id) ?? { x: centerX, y: centerY };
      return {
        ...node,
        x: center.x + Math.cos(angle) * ring,
        y: center.y + Math.sin(angle) * ring,
      };
    });
    const simulationEdges = resolveLayoutEdges(localEdges, nodes);
    nodesRef.current = nodes;
    edgesRef.current = resolveLayoutEdges(localEdges, nodes);
    clusterEdgesRef.current = clusterEdges;
    clusterCentersRef.current = centers.clusters;

    const centerFor = (node: LayoutNode) =>
      centers.nodes.get(node.id) ?? { x: centerX, y: centerY };
    const xForce = forceX<LayoutNode>((node) => centerFor(node).x).strength(
      (node) => (node.cluster < 0 ? 0.08 : 0.16),
    );
    const yForce = forceY<LayoutNode>((node) => centerFor(node).y).strength(
      (node) => (node.cluster < 0 ? 0.08 : 0.16),
    );
    const simulation = forceSimulation<LayoutNode>(nodes)
      .force(
        "link",
        forceLink<LayoutNode, LayoutEdge>(simulationEdges)
          .id((node) => node.id)
          .distance((edge) => 48 + (1 - edge.similarity) * 130)
          .strength((edge) => 0.24 + Math.max(0, edge.similarity) * 0.62),
      )
      .force("charge", forceManyBody<LayoutNode>().strength(-125).distanceMax(420))
      .force("center", forceCenter(centerX, centerY).strength(0.025))
      .force("cluster-x", xForce)
      .force("cluster-y", yForce)
      .force(
        "collision",
        forceCollide<LayoutNode>().radius((node) => nodeRadius(node) + 10),
      )
      .alphaDecay(0.028)
      .on("tick", () => {
        if (!viewTouchedRef.current) {
          transformRef.current = fitUniverseTransform(
            nodes.map((node) => ({
              x: node.x ?? centerX,
              y: node.y ?? centerY,
              radius: nodeRadius(node),
            })),
            canvas.width / (window.devicePixelRatio || 1),
            canvas.height / (window.devicePixelRatio || 1),
          );
        }
        render();
      });

    function resize() {
      const dpr = window.devicePixelRatio || 1;
      const nextWidth = Math.max(320, host!.clientWidth);
      const nextHeight = Math.max(480, host!.clientHeight);
      canvas!.width = Math.round(nextWidth * dpr);
      canvas!.height = Math.round(nextHeight * dpr);
      canvas!.style.width = `${nextWidth}px`;
      canvas!.style.height = `${nextHeight}px`;
      centers = layoutCenters(graphNodes, clusterEdges, nextWidth, nextHeight);
      clusterCentersRef.current = centers.clusters;
      xForce.x((node) => centerFor(node).x);
      yForce.y((node) => centerFor(node).y);
      simulation.force(
        "center",
        forceCenter(nextWidth / 2, nextHeight / 2).strength(0.025),
      );
      simulation.alpha(0.28).restart();
      if (!viewTouchedRef.current) {
        transformRef.current = fitUniverseTransform(
          nodes.map((node) => ({
            x: node.x ?? nextWidth / 2,
            y: node.y ?? nextHeight / 2,
            radius: nodeRadius(node),
          })),
          nextWidth,
          nextHeight,
        );
      }
      render();
    }

    const observer = new ResizeObserver(resize);
    observer.observe(host);
    resize();
    const themeObserver = new MutationObserver(render);
    themeObserver.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class", "style", "data-theme"],
    });
    const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
    let animationFrame = 0;
    const animate = () => {
      render();
      animationFrame = requestAnimationFrame(animate);
    };
    if (!reduceMotion.matches) animationFrame = requestAnimationFrame(animate);
    return () => {
      observer.disconnect();
      themeObserver.disconnect();
      simulation.stop();
      cancelAnimationFrame(animationFrame);
    };
  }, [graphNodes, layoutKey, render]);

  useEffect(() => {
    edgesRef.current = resolveLayoutEdges(renderedEdges, nodesRef.current);
    render();
  }, [render, renderedEdges]);

  const canvasPoint = useCallback(
    (event: { clientX: number; clientY: number }) => {
      const rect = canvasRef.current!.getBoundingClientRect();
      return { x: event.clientX - rect.left, y: event.clientY - rect.top };
    },
    [],
  );

  const hitNode = useCallback(
    (event: { clientX: number; clientY: number }): LayoutNode | null => {
      const point = canvasPoint(event);
      const transform = transformRef.current;
      const world = {
        x: (point.x - transform.x) / transform.k,
        y: (point.y - transform.y) / transform.k,
      };
      for (let index = nodesRef.current.length - 1; index >= 0; index--) {
        const node = nodesRef.current[index];
        if (node.x == null || node.y == null) continue;
        const dx = world.x - node.x;
        const dy = world.y - node.y;
        const radius = nodeRadius(node) + 7 / transform.k;
        if (dx * dx + dy * dy <= radius * radius) return node;
      }
      return null;
    },
    [canvasPoint],
  );

  const focusNode = useCallback(
    (id: string) => {
      const node = nodesRef.current.find((item) => item.id === id);
      const canvas = canvasRef.current;
      if (!node || !canvas || node.x == null || node.y == null) return;
      const dpr = window.devicePixelRatio || 1;
      const width = canvas.width / dpr;
      const height = canvas.height / dpr;
      const scale = Math.max(1.25, transformRef.current.k);
      viewTouchedRef.current = true;
      transformRef.current = {
        x: width / 2 - node.x * scale,
        y: height / 2 - node.y * scale,
        k: scale,
      };
      onSelect(id);
      render();
    },
    [onSelect, render],
  );

  const resetView = useCallback(() => {
    viewTouchedRef.current = false;
    onSelect(null);
    fitAllNodes();
  }, [fitAllNodes, onSelect]);

  useImperativeHandle(ref, () => ({ focusNode, resetView }), [focusNode, resetView]);

  function onPointerDown(event: ReactPointerEvent<HTMLCanvasElement>) {
    const node = hitNode(event);
    if (node) {
      onSelect(node.id);
      return;
    }
    viewTouchedRef.current = true;
    event.currentTarget.setPointerCapture(event.pointerId);
    const point = canvasPoint(event);
    gestureRef.current = {
      x: point.x,
      y: point.y,
      originX: transformRef.current.x,
      originY: transformRef.current.y,
    };
    event.currentTarget.style.cursor = "grabbing";
  }

  function onPointerMove(event: ReactPointerEvent<HTMLCanvasElement>) {
    const gesture = gestureRef.current;
    if (gesture) {
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
      event.currentTarget.style.cursor = node ? "pointer" : "grab";
      render();
    }
  }

  function endPointer(event: ReactPointerEvent<HTMLCanvasElement>) {
    gestureRef.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    event.currentTarget.style.cursor = hitNode(event) ? "pointer" : "grab";
  }

  function onWheel(event: ReactWheelEvent<HTMLCanvasElement>) {
    event.preventDefault();
    viewTouchedRef.current = true;
    const point = canvasPoint(event);
    const transform = transformRef.current;
    const nextScale = Math.max(
      0.12,
      Math.min(3.5, transform.k * Math.exp(-event.deltaY * 0.0012)),
    );
    const worldX = (point.x - transform.x) / transform.k;
    const worldY = (point.y - transform.y) / transform.k;
    transform.x = point.x - worldX * nextScale;
    transform.y = point.y - worldY * nextScale;
    transform.k = nextScale;
    render();
  }

  return (
    <canvas
      ref={canvasRef}
      aria-label={ariaLabel}
      className="block cursor-grab touch-none"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={endPointer}
      onPointerCancel={endPointer}
      onPointerLeave={() => {
        if (!gestureRef.current) {
          hoveredRef.current = null;
          render();
        }
      }}
      onDoubleClick={(event) => {
        const node = hitNode(event);
        if (node) window.location.href = browserViewHref(node.id);
      }}
      onWheel={onWheel}
    />
  );
});
