import { NextRequest } from "next/server";
import { readFile, stat } from "node:fs/promises";
import path from "node:path";
import { VAULT_PATH } from "@/lib/config";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const MIME: Record<string, string> = {
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".gif": "image/gif",
  ".webp": "image/webp",
  ".svg": "image/svg+xml",
  ".bmp": "image/bmp",
  ".avif": "image/avif",
  ".ico": "image/x-icon",
};

export async function GET(req: NextRequest) {
  const url = new URL(req.url);
  const raw = url.searchParams.get("path");
  if (!raw) return new Response("missing path", { status: 400 });

  const abs = path.resolve(raw);
  const ext = path.extname(abs).toLowerCase();
  if (!MIME[ext]) {
    return new Response("unsupported type", { status: 400 });
  }

  // Security: only serve images inside the vault
  if (
    !abs.startsWith(VAULT_PATH + path.sep) &&
    abs !== VAULT_PATH
  ) {
    return new Response("forbidden", { status: 403 });
  }

  try {
    const s = await stat(abs);
    if (!s.isFile()) return new Response("not a file", { status: 404 });
    const buf = await readFile(abs);
    return new Response(buf, {
      status: 200,
      headers: {
        "Content-Type": MIME[ext],
        "Cache-Control": "private, max-age=300",
        "Content-Length": String(buf.byteLength),
      },
    });
  } catch {
    return new Response("not found", { status: 404 });
  }
}
