import type { Collision } from "./sparks.ts";
import type { RelatedDocument } from "./universe.ts";

export type ReadingContext = {
  slug: string;
  title: string;
  anchor?: string;
};

export type EmergenceItem = {
  id: string;
  kind: "reflection" | "question" | "connection" | "related";
  title: string;
  body: string;
  detail?: string;
  provenance: string;
  quote?: string;
  relatedSlug?: string;
  relatedTitle?: string;
  score?: number;
};

export function buildEmergenceFeed(
  context: ReadingContext,
  related: readonly RelatedDocument[],
  collisions: readonly Collision[],
  limit = 3,
): EmergenceItem[] {
  const collisionOther = (collision: Collision) => {
    if (collision.slug_a === context.slug) {
      return { slug: collision.slug_b, title: collision.title_b };
    }
    if (collision.slug_b === context.slug) {
      return { slug: collision.slug_a, title: collision.title_a };
    }
    return null;
  };
  const dismissed = new Set(
    collisions
      .filter((collision) => collision.verdict === "dismissed")
      .map(collisionOther)
      .filter((other) => other !== null)
      .map((other) => other.slug),
  );
  const rankedCollisions = collisions
    .filter((collision) => collision.verdict !== "dismissed")
    .map((collision) => ({ collision, other: collisionOther(collision) }))
    .filter(
      (candidate): candidate is {
        collision: Collision;
        other: { slug: string; title: string };
      } => candidate.other !== null,
    )
    .sort((left, right) => {
      const questionOrder = Number(Boolean(right.collision.question.trim())) -
        Number(Boolean(left.collision.question.trim()));
      if (questionOrder !== 0) return questionOrder;
      const verdictOrder = Number(right.collision.verdict === "confirmed") -
        Number(left.collision.verdict === "confirmed");
      if (verdictOrder !== 0) return verdictOrder;
      return right.collision.score - left.collision.score;
    });

  const items: EmergenceItem[] = rankedCollisions.map(
    ({ collision, other }) => {
      const question = collision.question.trim();
      const explanation = collision.explanation.trim();
      return {
        id: `collision:${collision.id}`,
        kind: question ? "question" : "connection",
        title: question ? "值得追问" : "出现了一条连接",
        body:
          question || explanation || "这两篇内容之间可能存在值得检查的连接。",
        ...(question && explanation ? { detail: explanation } : {}),
        provenance:
          collision.verdict === "confirmed" ? "已确认碰撞" : "内容碰撞",
        relatedSlug: other.slug,
        relatedTitle: other.title,
        score: collision.score,
      };
    },
  );

  const represented = new Set([
    ...dismissed,
    ...rankedCollisions.map(({ other }) => other.slug),
  ]);
  for (const document of [...related].sort(
    (left, right) => right.similarity - left.similarity,
  )) {
    if (items.length >= limit) break;
    if (represented.has(document.slug)) continue;
    represented.add(document.slug);
    items.push({
      id: `related:${document.slug}`,
      kind: "related",
      title: "继续阅读",
      body: "这篇文档与当前内容在语义上接近。",
      provenance: "语义邻居",
      relatedSlug: document.slug,
      relatedTitle: document.title,
      score: document.similarity,
    });
  }

  if (items.length > 0) return items.slice(0, Math.max(1, limit));
  return [
    {
      id: `reflection:${context.slug}`,
      kind: "reflection",
      title: "换一个角度",
      body: "这段内容依赖了什么前提？如果前提不成立，结论会怎样变化？",
      provenance: "当前阅读",
      ...(context.anchor ? { quote: context.anchor } : {}),
    },
  ];
}
