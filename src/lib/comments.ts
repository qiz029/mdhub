import { API_URL } from "./config";

export type CommentEntry = {
  author: string;
  time: string; // "YYYY-MM-DD HH:mm"
  text: string;
};

export type CommentThread = {
  id: string;
  quote: string; // anchored text from the note
  prefix: string; // ~40 chars before the quote, for disambiguation
  suffix: string; // ~40 chars after
  comments: CommentEntry[];
};

function encodeSlug(slug: string): string {
  return slug
    .split("/")
    .map((s) => encodeURIComponent(s))
    .join("/");
}

export async function getComments(slug: string): Promise<CommentThread[]> {
  try {
    const res = await fetch(
      `${API_URL}/api/documents/${encodeSlug(slug)}/comments`,
      { cache: "no-store" },
    );
    if (!res.ok) return [];
    return await res.json();
  } catch {
    return [];
  }
}

export type NewComment = {
  author: string;
  text: string;
  anchor?: { quote: string; prefix: string; suffix: string };
  reply?: string;
};

export async function appendComment(
  slug: string,
  c: NewComment,
): Promise<{ id: string }> {
  const body: Record<string, unknown> = { author: c.author, text: c.text };
  if (c.reply) {
    body.reply = c.reply;
  } else if (c.anchor) {
    body.anchor = c.anchor;
  }
  const res = await fetch(
    `${API_URL}/api/documents/${encodeSlug(slug)}/comments`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      cache: "no-store",
    },
  );
  if (!res.ok) {
    let msg = `comment failed (${res.status})`;
    try {
      const data = await res.json();
      if (data?.error) msg = String(data.error);
    } catch {
      // keep default message
    }
    throw new Error(msg);
  }
  const data = await res.json();
  return { id: String(data.id) };
}
