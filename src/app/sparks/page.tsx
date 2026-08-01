import { Nav } from "@/components/Nav";
import { SparksClient } from "@/components/SparksClient";

export const dynamic = "force-dynamic";

export default function SparksPage() {
  return (
    <div>
      <Nav active="sparks" />
      <main className="mx-auto max-w-3xl px-4 py-6 sm:px-6 md:py-8">
        <div className="mb-6">
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-stone-400">
            Fleeting notes
          </p>
          <h1 className="mt-2 text-2xl font-bold tracking-tight text-stone-900 sm:text-3xl">
            Sparks
          </h1>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-stone-500">
            碎片 → 碰撞 → 灵感 → 长成。这里的内容是私密的，只有持编辑令牌可见。
          </p>
        </div>
        <SparksClient />
      </main>
    </div>
  );
}
