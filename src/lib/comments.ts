import { mkdir, readFile, appendFile } from "node:fs/promises";
import path from "node:path";
import { VAULT_PATH } from "./config";

export type CommentEntry = {
  author: string;
  time: string; // "YYYY-MM-DD HH:mm" (local)
  text: string;
};

export type CommentThread = {
  id: string;
  quote: string; // anchored text from the note
  prefix: string; // ~40 chars before the quote, for disambiguation
  suffix: string; // ~40 chars after
  comments: CommentEntry[];
};

// Comments live in the vault at _comments/<slug>.md, mirroring the note
// path. The file is plain markdown — one H2 section per comment, anchor
// metadata in an HTML comment — so it stays readable/editable in Obsidian
// and writable by the agent:
//
//   ---
//   note: notes/travel
//   ---
//
//   ## 用户 · 2026-07-20 21:30
//   <!-- {"id":"k3x9ab","quote":"路线包括大理","prefix":"...","suffix":"..."} -->
//   住宿定了吗？
//
//   ## Hermes · 2026-07-20 21:45
//   <!-- {"reply":"k3x9ab"} -->
//   今晚看。
//
// Human comments are stamped "用户" by the API; the agent writes under its
// own name, either via POST /api/comments or by appending a section here.

const HEADING = /^## (.*?) · (\d{4}-\d{2}-\d{2} \d{2}:\d{2})\s*$/;

export function commentsPath(slug: string): string {
  const clean = slug.replace(/\.\./g, "").replace(/\\/g, "/");
  return path.join(VAULT_PATH, "_comments", `${clean}.md`);
}

type RawSection = {
  author: string;
  time: string;
  meta: Record<string, string>;
  text: string;
};

function parseSections(md: string): RawSection[] {
  // Split on heading lines that carry a full "author · date" pattern, so a
  // literal "## ..." inside a comment body can't break parsing.
  const parts = md.split(/\n(?=## .* · \d{4}-\d{2}-\d{2} \d{2}:\d{2})/);
  const out: RawSection[] = [];
  for (const part of parts) {
    const lines = part.split("\n");
    const m = lines[0].match(HEADING);
    if (!m) continue;
    let rest = lines.slice(1).join("\n").trim();
    let meta: Record<string, string> = {};
    const metaMatch = rest.match(/^<!--\s*(\{[\s\S]*?\})\s*-->/);
    if (metaMatch) {
      try {
        meta = JSON.parse(metaMatch[1]);
      } catch {
        meta = {};
      }
      rest = rest.slice(metaMatch[0].length).trim();
    }
    out.push({ author: m[1], time: m[2], meta, text: rest });
  }
  return out;
}

export function parseComments(md: string): CommentThread[] {
  const threads: CommentThread[] = [];
  const byId = new Map<string, CommentThread>();
  for (const s of parseSections(md)) {
    if (s.meta.reply) {
      const t = byId.get(s.meta.reply);
      if (t) {
        t.comments.push({ author: s.author, time: s.time, text: s.text });
      }
      continue;
    }
    if (!s.meta.id || !s.meta.quote) continue;
    const t: CommentThread = {
      id: s.meta.id,
      quote: s.meta.quote,
      prefix: s.meta.prefix || "",
      suffix: s.meta.suffix || "",
      comments: [{ author: s.author, time: s.time, text: s.text }],
    };
    threads.push(t);
    byId.set(t.id, t);
  }
  return threads;
}

export async function getComments(slug: string): Promise<CommentThread[]> {
  try {
    const raw = await readFile(commentsPath(slug), "utf8");
    return parseComments(raw);
  } catch {
    return [];
  }
}

function nowLocal(): string {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

export type NewComment = {
  author: string;
  text: string;
  anchor?: { quote: string; prefix: string; suffix: string };
  reply?: string;
};

// appendComment writes one H2 section. Returns the thread id (new id for a
// fresh anchor, the existing id for a reply).
export async function appendComment(
  slug: string,
  c: NewComment,
): Promise<{ id: string }> {
  const filePath = commentsPath(slug);
  await mkdir(path.dirname(filePath), { recursive: true });

  try {
    await readFile(filePath, "utf8");
  } catch {
    await appendFile(filePath, `---\nnote: ${slug}\n---\n`, "utf8");
  }

  let meta: Record<string, string>;
  let id: string;
  if (c.reply) {
    meta = { reply: c.reply };
    id = c.reply;
  } else {
    id = Math.random().toString(36).slice(2, 8);
    meta = {
      id,
      quote: c.anchor?.quote || "",
      prefix: c.anchor?.prefix || "",
      suffix: c.anchor?.suffix || "",
    };
  }

  const section = `\n## ${c.author} · ${nowLocal()}\n<!-- ${JSON.stringify(meta)} -->\n${c.text.trim()}\n`;
  await appendFile(filePath, section, "utf8");
  return { id };
}
