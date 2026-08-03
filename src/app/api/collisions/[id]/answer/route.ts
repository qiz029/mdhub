import { NextRequest } from "next/server";
import { API_URL } from "@/lib/config";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function POST(
  req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
) {
  const { id } = await params;
  if (!/^\d+$/.test(id)) {
    return Response.json({ error: "invalid collision id" }, { status: 400 });
  }

  try {
    const upstream = await fetch(
      `${API_URL}/api/collisions/${encodeURIComponent(id)}/answer`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: await req.text(),
        cache: "no-store",
      },
    );
    const body = await upstream.text();
    return new Response(body, {
      status: upstream.status,
      headers: {
        "Content-Type":
          upstream.headers.get("Content-Type") || "application/json",
        "Cache-Control": "no-store",
      },
    });
  } catch {
    return Response.json({ error: "upstream unavailable" }, { status: 502 });
  }
}
