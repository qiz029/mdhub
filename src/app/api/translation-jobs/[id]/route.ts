import { API_URL } from "@/lib/config";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

function validID(id: string): boolean {
  return /^[a-z0-9-]{1,80}$/.test(id);
}

export async function GET(
  _req: Request,
  { params }: { params: Promise<{ id: string }> },
) {
  const { id } = await params;
  if (!validID(id)) {
    return Response.json({ error: "invalid translation job id" }, { status: 400 });
  }
  try {
    const upstream = await fetch(
      `${API_URL}/api/translation-jobs/${encodeURIComponent(id)}`,
      { cache: "no-store" },
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
