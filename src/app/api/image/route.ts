import { NextRequest } from "next/server";
import { API_URL } from "@/lib/config";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

// Pure proxy: forwards vault-relative image keys to the Go backend.
export async function GET(req: NextRequest) {
  const url = new URL(req.url);
  const key = url.searchParams.get("path");
  if (!key || key.includes("..")) {
    return new Response("bad path", { status: 400 });
  }

  try {
    const upstream = await fetch(
      `${API_URL}/api/images?path=${encodeURIComponent(key)}`,
      { cache: "no-store" },
    );
    const headers = new Headers();
    const contentType = upstream.headers.get("Content-Type");
    if (contentType) headers.set("Content-Type", contentType);
    headers.set(
      "Cache-Control",
      upstream.headers.get("Cache-Control") || "private, max-age=300",
    );
    return new Response(upstream.body, {
      status: upstream.status,
      headers,
    });
  } catch {
    return new Response("upstream unavailable", { status: 502 });
  }
}
