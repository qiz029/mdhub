import { Nav } from "@/components/Nav";
import { TranslationDashboard } from "@/components/TranslationDashboard";

export const dynamic = "force-dynamic";

export default function TranslationsPage() {
  return (
    <div>
      <Nav active="translations" />
      <main className="mx-auto max-w-4xl px-4 py-8 sm:px-6 md:py-10">
        <div className="mb-8">
          <p className="text-xs font-semibold uppercase tracking-[0.18em] text-stone-400">
            Agent pipeline
          </p>
          <h1 className="mt-2 text-2xl font-bold tracking-tight text-stone-900 sm:text-3xl">
            论文翻译
          </h1>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-stone-500">
            粘贴论文地址，Agent 获取全文、完整翻译、校验后生成 MDHub 草稿。
          </p>
        </div>
        <TranslationDashboard />
      </main>
    </div>
  );
}
