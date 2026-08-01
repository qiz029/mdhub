import { NextRequest } from "next/server";
import { API_URL } from "@/lib/config";
import { requireEditToken } from "@/lib/edit-auth";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";
const MAX_IMAGE_REQUEST_BYTES = 21 * 1024 * 1024;

// Proxy image reads and validated browser uploads to the Go backend.
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
    headers.set("Content-Security-Policy", "default-src 'none'; sandbox");
    headers.set("X-Content-Type-Options", "nosniff");
    headers.set("Cross-Origin-Resource-Policy", "same-origin");
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

export async function POST(req: NextRequest) {
  const unauthorized = requireEditToken(req);
  if (unauthorized) return unauthorized;

  const contentType = req.headers.get("Content-Type") || "";
  if (!contentType.toLowerCase().startsWith("multipart/form-data")) {
    return Response.json({ error: "invalid multipart upload" }, { status: 400 });
  }
  const declaredLength = Number(req.headers.get("Content-Length"));
  if (Number.isFinite(declaredLength) && declaredLength > MAX_IMAGE_REQUEST_BYTES) {
    return Response.json({ error: "image upload exceeds 20 MB" }, { status: 413 });
  }
  if (!req.body) {
    return Response.json({ error: "missing image file" }, { status: 400 });
  }

  try {
    const upstream = await fetch(`${API_URL}/api/images`, {
      method: "POST",
      headers: {
        "Content-Type": contentType,
        "X-MDHub-Edit-Token": process.env.MDHUB_EDIT_TOKEN || "",
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
