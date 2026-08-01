"use client";

import { useEffect, useState } from "react";
import type { GrowthDay, GrowthResponse } from "@/lib/play";

const WIDTH = 600;
const HEIGHT = 180;
const PAD = 8;

function points(
  days: GrowthDay[],
  pick: (day: GrowthDay) => number,
  max: number,
): string {
  return days
    .map((day, i) => {
      const x = PAD + (i / (days.length - 1)) * (WIDTH - 2 * PAD);
      const y =
        HEIGHT - PAD - (pick(day) / max) * (HEIGHT - 2 * PAD);
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
}

function TotalCard({ label, value }: { label: string; value: number }) {
  return (
    <div className="rounded-xl border border-stone-200 bg-white px-4 py-3">
      <p className="text-xs text-stone-400">{label}</p>
      <p className="mt-1 text-xl font-semibold tabular-nums text-stone-900">
        {value}
      </p>
    </div>
  );
}

// Growth visualization: cumulative sparks/collisions curves (hand-drawn SVG,
// no chart dependency) plus a totals row. Reads are public.
export function GrowthChart() {
  const [data, setData] = useState<GrowthResponse | null>(null);

  useEffect(() => {
    let cancelled = false;
    fetch("/mdhub/api/growth")
      .then((res) => (res.ok ? res.json() : null))
      .then((json: GrowthResponse | null) => {
        if (!cancelled && json && Array.isArray(json.days)) setData(json);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);

  if (!data) return null;

  const { days, totals } = data;
  const max = Math.max(
    1,
    ...days.map((d) => Math.max(d.sparks_total, d.collisions_total)),
  );

  return (
    <section aria-labelledby="growth-title">
      <h2 id="growth-title" className="text-sm font-semibold text-stone-800">
        成长
      </h2>
      <div className="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-5">
        <TotalCard label="碎片" value={totals.sparks} />
        <TotalCard label="碰撞" value={totals.collisions} />
        <TotalCard label="已确认" value={totals.confirmed} />
        <TotalCard label="已回答悬赏" value={totals.answered} />
        <TotalCard label="正式笔记" value={totals.notes} />
      </div>
      {days.length < 2 ? (
        <p className="mt-3 rounded-xl border border-stone-200 bg-white px-4 py-8 text-center text-sm text-stone-400">
          数据还在积累，过几天再来看曲线。
        </p>
      ) : (
        <div className="mt-3 rounded-xl border border-stone-200 bg-white p-4">
          <svg
            viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
            role="img"
            aria-label="碎片与碰撞的累计曲线"
            className="w-full"
          >
            <polyline
              points={points(days, (d) => d.collisions_total, max)}
              fill="none"
              stroke="#d97706"
              strokeWidth="2"
              strokeLinejoin="round"
              strokeLinecap="round"
            />
            <polyline
              points={points(days, (d) => d.sparks_total, max)}
              fill="none"
              stroke="#1c1917"
              strokeWidth="2"
              strokeLinejoin="round"
              strokeLinecap="round"
            />
          </svg>
          <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-stone-500">
            <span className="inline-flex items-center gap-1.5">
              <span className="inline-block h-0.5 w-4 rounded bg-stone-900" />
              碎片累计
            </span>
            <span className="inline-flex items-center gap-1.5">
              <span className="inline-block h-0.5 w-4 rounded bg-amber-600" />
              碰撞累计
            </span>
            <span className="ml-auto tabular-nums text-stone-400">
              {days[0].date} → {days[days.length - 1].date}
            </span>
          </div>
        </div>
      )}
    </section>
  );
}
