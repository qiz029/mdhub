import { NextRequest } from "next/server";
import { API_URL } from "@/lib/config";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const MAX_DOCUMENT_BYTES = 32 * 1024 * 1024;

export async function PUT(
  req: NextRequest,
  { params }: { params: Promise<{ slug: string[] }> },
) {
  const { slug: segments } = await params;
  const contentLength = Number(req.headers.get("content-length"));
  if (Number.isFinite(contentLength) && contentLength > MAX_DOCUMENT_BYTES) {
    return Response.json({ error: "document exceeds 32 MB" }, { status: 413 });
  }
  if (!req.body) {
    return Response.json({ error: "missing document body" }, { status: 400 });
  }
  const slug = segments.map((part) => encodeURIComponent(part)).join("/");

  try {
    const upstream = await fetch(`${API_URL}/api/documents/${slug}`, {
      method: "PUT",
      headers: {
        "Content-Type": "text/markdown; charset=utf-8",
      },
      body: req.body,
      cache: "no-store",
      duplex: "half",
    } as RequestInit & { duplex: "half" });
    const body = await upstream.text();
    return new Response(body, {
      status: upstream.status,
      headers: {
        "Content-Type": upstream.headers.get("Content-Type") || "application/json",
        "Cache-Control": "no-store",
      },
    });
  } catch {
    return Response.json({ error: "upstream unavailable" }, { status: 502 });
  }
}
