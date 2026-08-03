import { API_URL } from "@/lib/config";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const ACTIONS = new Set(["cancel", "retry", "publish"]);

export async function POST(
  _req: Request,
  { params }: { params: Promise<{ id: string; action: string }> },
) {
  const { id, action } = await params;
  if (!/^[a-z0-9-]{1,80}$/.test(id) || !ACTIONS.has(action)) {
    return Response.json({ error: "invalid translation action" }, { status: 400 });
  }
  try {
    const upstream = await fetch(
      `${API_URL}/api/translation-jobs/${encodeURIComponent(id)}/${action}`,
      { method: "POST", cache: "no-store" },
    );
    return new Response(await upstream.text(), {
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
