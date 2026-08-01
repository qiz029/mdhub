import { NextRequest } from "next/server";
import { getPublishedFile } from "@/lib/vault";
import { appendComment, CommentAPIError } from "@/lib/comments";
import {
  CommentRequestError,
  normalizeCommentRequest,
} from "@/lib/comment-request";
import { readLimitedJSON, RequestBodyError } from "@/lib/request-body";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const MAX_COMMENT_BODY_BYTES = 16 * 1024;

export async function POST(req: NextRequest) {
  let decoded: unknown;
  try {
    decoded = await readLimitedJSON(req, MAX_COMMENT_BODY_BYTES);
  } catch (error) {
    if (error instanceof RequestBodyError) {
      return Response.json({ error: error.message }, { status: error.status });
    }
    return Response.json({ error: "invalid json" }, { status: 400 });
  }

  let normalized: ReturnType<typeof normalizeCommentRequest>;
  try {
    normalized = normalizeCommentRequest(decoded);
  } catch (error) {
    if (error instanceof CommentRequestError) {
      return Response.json({ error: error.message }, { status: error.status });
    }
    return Response.json({ error: "invalid comment" }, { status: 400 });
  }
  const { slug, comment } = normalized;

  // Only published notes can be commented on (also sanitizes the slug).
  const file = await getPublishedFile(slug);
  if (!file) return Response.json({ error: "not found" }, { status: 404 });

  try {
    const { id } = await appendComment(slug, comment);
    return Response.json({ ok: true, id });
  } catch (error) {
    if (error instanceof CommentAPIError) {
      return Response.json({ error: error.message }, { status: error.status });
    }
    console.error("comment upstream unavailable", error);
    return Response.json({ error: "comment service unavailable" }, { status: 502 });
  }
}
