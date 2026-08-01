import { Nav } from "@/components/Nav";
import { UniverseGraphView } from "@/components/UniverseGraph";
import { getUniverse } from "@/lib/universe";

export const dynamic = "force-dynamic";

export default async function UniversePage() {
  const graph = await getUniverse();

  return (
    <div>
      <Nav active="universe" />
      <main className="mx-auto max-w-[90rem] px-4 py-6 sm:px-6 md:py-8">
        <div className="mb-6">
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-stone-400">
            Semantic map
          </p>
          <h1 className="mt-2 text-2xl font-bold tracking-tight text-stone-900 sm:text-3xl">
            Knowledge Universe
          </h1>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-stone-500">
            文档之间的距离由语义相似度决定。越相似的内容，位置越近，连线越强。
          </p>
        </div>

        {graph ? (
          <UniverseGraphView graph={graph} />
        ) : (
          <section className="rounded-2xl border border-stone-200 bg-stone-50 px-6 py-20 text-center">
            <p className="font-medium text-stone-700">暂时无法加载知识宇宙</p>
            <p className="mt-2 text-sm text-stone-400">
              请确认 Go backend 已升级并且 /api/universe 可以访问。
            </p>
          </section>
        )}
      </main>
    </div>
  );
}
