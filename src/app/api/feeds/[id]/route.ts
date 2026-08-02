import { NextRequest } from "next/server";
import { API_URL } from "@/lib/config";
import { requireEditToken } from "@/lib/edit-auth";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

async function proxy(
  req: NextRequest,
  id: string,
  method: "POST" | "DELETE",
): Promise<Response> {
  const unauthorized = requireEditToken(req);
  if (unauthorized) return unauthorized;
  if (!/^\d+$/.test(id)) {
    return Response.json({ error: "invalid feed id" }, { status: 400 });
  }

  try {
    const upstream = await fetch(
      `${API_URL}/api/feeds/${encodeURIComponent(id)}`,
      {
        method,
        headers: {
          "Content-Type": "application/json",
          "X-MDHub-Edit-Token": process.env.MDHUB_EDIT_TOKEN || "",
        },
        body: method === "POST" ? await req.text() : undefined,
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

export async function POST(
  req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
) {
  const { id } = await params;
  return proxy(req, id, "POST");
}

export async function DELETE(
  req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
) {
  const { id } = await params;
  return proxy(req, id, "DELETE");
}
