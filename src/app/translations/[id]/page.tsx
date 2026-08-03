import Link from "next/link";
import { Nav } from "@/components/Nav";
import { TranslationJobClient } from "@/components/TranslationJobClient";

export const dynamic = "force-dynamic";

export default async function TranslationJobPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;
  return (
    <div>
      <Nav active="translations" />
      <main className="mx-auto max-w-6xl px-4 py-8 sm:px-6 md:py-10">
        <Link href="/translations" className="mb-5 inline-block text-sm text-stone-400 hover:text-stone-700">
          ← 返回翻译任务
        </Link>
        <TranslationJobClient id={id} />
      </main>
    </div>
  );
}
