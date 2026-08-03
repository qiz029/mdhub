import { API_URL } from "@/lib/config";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const ACTIONS = new Set(["cancel", "retry", "publish", "source"]);
const MAX_PDF_REQUEST_BYTES = 51 * 1024 * 1024;

export async function POST(
  req: Request,
  { params }: { params: Promise<{ id: string; action: string }> },
) {
  const { id, action } = await params;
  if (!/^[a-z0-9-]{1,80}$/.test(id) || !ACTIONS.has(action)) {
    return Response.json({ error: "invalid translation action" }, { status: 400 });
  }
  if (action === "source") {
    const contentType = req.headers.get("Content-Type") || "";
    if (!contentType.toLowerCase().startsWith("multipart/form-data")) {
      return Response.json({ error: "invalid multipart upload" }, { status: 400 });
    }
    const declaredLength = Number(req.headers.get("Content-Length"));
    if (Number.isFinite(declaredLength) && declaredLength > MAX_PDF_REQUEST_BYTES) {
      return Response.json({ error: "PDF upload exceeds 50 MB" }, { status: 413 });
    }
    if (!req.body) {
      return Response.json({ error: "missing PDF file" }, { status: 400 });
    }
    try {
      const upstream = await fetch(
        `${API_URL}/api/translation-jobs/${encodeURIComponent(id)}/source`,
        {
          method: "POST",
          headers: { "Content-Type": contentType },
          body: req.body,
          cache: "no-store",
          duplex: "half",
        } as RequestInit & { duplex: "half" },
      );
      return new Response(await upstream.text(), {
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
