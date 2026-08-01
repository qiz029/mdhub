import { createHash, timingSafeEqual } from "node:crypto";
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
  if (!editTokenMatches(provided, expected)) {
    return Response.json({ error: "invalid edit token" }, { status: 401 });
  }
  return null;
}

export function editTokenMatches(provided: string, expected: string): boolean {
  if (!expected) return false;
  const providedHash = createHash("sha256").update(provided).digest();
  const expectedHash = createHash("sha256").update(expected).digest();
  return timingSafeEqual(providedHash, expectedHash);
}
