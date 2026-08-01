import type { NewComment } from "./comments";

export class CommentRequestError extends Error {
  readonly status = 400;

  constructor(message: string) {
    super(message);
    this.name = "CommentRequestError";
  }
}

export type NormalizedCommentRequest = {
  slug: string;
  comment: NewComment;
};

function objectRecord(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function runeLength(value: string): number {
  return [...value].length;
}

function boundedOptionalString(
  value: unknown,
  field: string,
  limit: number,
): string {
  if (value === undefined || value === null) return "";
  if (typeof value !== "string") {
    throw new CommentRequestError(`${field} must be a string`);
  }
  if (runeLength(value) > limit) {
    throw new CommentRequestError(`${field} too long`);
  }
  return value;
}

// normalizeCommentRequest is the narrow validation seam between untrusted JSON
// and the comment API client. The route adapter never reads unchecked fields.
export function normalizeCommentRequest(input: unknown): NormalizedCommentRequest {
  const body = objectRecord(input);
  if (!body) throw new CommentRequestError("invalid json");

  const slug = boundedOptionalString(body.slug, "slug", 1_000).trim();
  if (!slug) throw new CommentRequestError("missing slug");
  const text = boundedOptionalString(body.text, "text", 2_000).trim();
  if (!text) throw new CommentRequestError("missing text");
  const author = boundedOptionalString(body.author, "author", 30).trim() || "用户";
  const reply = boundedOptionalString(body.reply, "reply", 20).trim();

  const comment: NewComment = { author, text };
  if (reply) {
    comment.reply = reply;
    return { slug, comment };
  }

  const anchor = objectRecord(body.anchor);
  if (!anchor) throw new CommentRequestError("missing anchor quote");
  const quote = boundedOptionalString(anchor.quote, "quote", 500).trim();
  if (!quote) throw new CommentRequestError("missing anchor quote");
  comment.anchor = {
    quote,
    prefix: boundedOptionalString(anchor.prefix, "prefix", 80),
    suffix: boundedOptionalString(anchor.suffix, "suffix", 80),
  };
  return { slug, comment };
}
