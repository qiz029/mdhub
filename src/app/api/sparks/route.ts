import { NextRequest } from "next/server";
import { API_URL } from "@/lib/config";
import { requireEditToken } from "@/lib/edit-auth";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function GET(req: NextRequest) {
  const unauthorized = requireEditToken(req);
  if (unauthorized) return unauthorized;

  try {
    const upstream = await fetch(`${API_URL}/api/sparks`, {
      headers: {
        "X-MDHub-Edit-Token": process.env.MDHUB_EDIT_TOKEN || "",
      },
      cache: "no-store",
    });
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
