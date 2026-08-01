import { timingSafeEqual } from "node:crypto";
import type { NextRequest } from "next/server";

export function requireEditToken(req: NextRequest): Response | null {
  const expected = process.env.MDHUB_EDIT_TOKEN || "";
  if (!expected) {
    return Response.json(
      { error: "editing is disabled; configure MDHUB_EDIT_TOKEN" },
      { status: 503 },
    );
  }
  const provided = req.headers.get("x-mdhub-edit-token") || "";
  const expectedBytes = Buffer.from(expected);
  const providedBytes = Buffer.from(provided);
  if (
    expectedBytes.length !== providedBytes.length ||
    !timingSafeEqual(expectedBytes, providedBytes)
  ) {
    return Response.json({ error: "invalid edit token" }, { status: 401 });
  }
  return null;
}
