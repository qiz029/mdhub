import { NextRequest } from "next/server";
import { API_URL } from "@/lib/config";
import { readLimitedJSON, RequestBodyError } from "@/lib/request-body";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

function passThrough(upstream: globalThis.Response, body: string): Response {
  return new Response(body, {
    status: upstream.status,
    headers: {
      "Content-Type":
        upstream.headers.get("Content-Type") || "application/json",
      "Cache-Control": "no-store",
    },
  });
}

export async function GET() {
  try {
    const upstream = await fetch(`${API_URL}/api/translation-jobs`, {
      cache: "no-store",
    });
    return passThrough(upstream, await upstream.text());
  } catch {
    return Response.json({ error: "upstream unavailable" }, { status: 502 });
  }
}

export async function POST(req: NextRequest) {
  try {
    const input = await readLimitedJSON(req, 32 * 1024);
    const upstream = await fetch(`${API_URL}/api/translation-jobs`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    return passThrough(upstream, await upstream.text());
  } catch (error) {
    if (error instanceof RequestBodyError) {
      return Response.json({ error: error.message }, { status: error.status });
    }
    return Response.json({ error: "upstream unavailable" }, { status: 502 });
  }
}
