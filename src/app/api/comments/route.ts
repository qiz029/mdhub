import { NextRequest } from "next/server";
import { getPublishedFile } from "@/lib/vault";
import { appendComment, type NewComment } from "@/lib/comments";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

function bad(msg: string) {
  return Response.json({ error: msg }, { status: 400 });
}

export async function POST(req: NextRequest) {
  let body: {
    slug?: string;
    author?: string;
    text?: string;
    anchor?: { quote?: string; prefix?: string; suffix?: string };
    reply?: string;
  };
  try {
    body = await req.json();
  } catch {
    return bad("invalid json");
  }

  const slug = (body.slug || "").trim();
  // Author is optional: human comments default to "用户"; the agent passes
  // its own name (or appends to the sidecar file directly).
  const author = (body.author || "").trim().slice(0, 30) || "用户";
  const text = (body.text || "").trim();
  if (!slug) return bad("missing slug");
  if (!text) return bad("missing text");
  if (text.length > 2000) return bad("text too long");

  // Only published notes can be commented on (also sanitizes the slug).
  const file = await getPublishedFile(slug);
  if (!file) return Response.json({ error: "not found" }, { status: 404 });

  const c: NewComment = { author, text };
  if (body.reply) {
    c.reply = String(body.reply).slice(0, 20);
  } else {
    const quote = (body.anchor?.quote || "").trim();
    if (!quote) return bad("missing anchor quote");
    if (quote.length > 500) return bad("quote too long");
    c.anchor = {
      quote,
      prefix: (body.anchor?.prefix || "").slice(0, 80),
      suffix: (body.anchor?.suffix || "").slice(0, 80),
    };
  }

  try {
    const { id } = await appendComment(slug, c);
    return Response.json({ ok: true, id });
  } catch (e) {
    return Response.json({ error: String(e) }, { status: 500 });
  }
}
