import assert from "node:assert/strict";
import test from "node:test";
import { buildEmergenceFeed } from "./emergence.ts";

test("a single document produces a grounded cold-start reflection", () => {
  const items = buildEmergenceFeed(
    {
      slug: "papers/memory",
      title: "Memory Is the New Database",
      anchor: "Long-term memory changes what an agent can learn.",
    },
    [],
    [],
  );

  assert.deepEqual(items, [
    {
      id: "reflection:papers/memory",
      kind: "reflection",
      title: "换一个角度",
      body: "这段内容依赖了什么前提？如果前提不成立，结论会怎样变化？",
      provenance: "当前阅读",
      quote: "Long-term memory changes what an agent can learn.",
    },
  ]);
});

test("collision questions lead the feed and related documents are deduplicated", () => {
  const items = buildEmergenceFeed(
    { slug: "notes/a", title: "A" },
    [
      { slug: "notes/c", title: "C", similarity: 0.97 },
      { slug: "notes/e", title: "E", similarity: 0.82 },
      { slug: "notes/b", title: "B", similarity: 0.8 },
    ],
    [
      {
        id: 1,
        slug_a: "notes/a",
        slug_b: "notes/c",
        title_a: "A",
        title_b: "C",
        score: 0.91,
        explanation: "A 和 C 对长期记忆持有不同假设。",
        question: "变化的是检索目标，还是记忆本身？",
        verdict: "new",
        created_at: 1,
        answered_by: "",
        answered_at: 0,
      },
      {
        id: 2,
        slug_a: "notes/d",
        slug_b: "notes/a",
        title_a: "D",
        title_b: "A",
        score: 0.88,
        explanation: "D 为 A 提供了一个反例。",
        question: "",
        verdict: "confirmed",
        created_at: 2,
        answered_by: "",
        answered_at: 0,
      },
      {
        id: 3,
        slug_a: "notes/a",
        slug_b: "notes/b",
        title_a: "A",
        title_b: "B",
        score: 0.99,
        explanation: "已被用户忽略。",
        question: "为什么？",
        verdict: "dismissed",
        created_at: 3,
        answered_by: "",
        answered_at: 0,
      },
    ],
  );

  assert.deepEqual(items, [
    {
      id: "collision:1",
      kind: "question",
      title: "值得追问",
      body: "变化的是检索目标，还是记忆本身？",
      detail: "A 和 C 对长期记忆持有不同假设。",
      provenance: "内容碰撞",
      relatedSlug: "notes/c",
      relatedTitle: "C",
      score: 0.91,
    },
    {
      id: "collision:2",
      kind: "connection",
      title: "出现了一条连接",
      body: "D 为 A 提供了一个反例。",
      provenance: "已确认碰撞",
      relatedSlug: "notes/d",
      relatedTitle: "D",
      score: 0.88,
    },
    {
      id: "related:notes/e",
      kind: "related",
      title: "继续阅读",
      body: "这篇文档与当前内容在语义上接近。",
      provenance: "语义邻居",
      relatedSlug: "notes/e",
      relatedTitle: "E",
      score: 0.82,
    },
  ]);
});
