export type SearchSnippetSegment = {
  text: string;
  highlighted: boolean;
};

function decodeEscapedText(value: string): string {
  return value.replace(
    /&(?:#(\d+)|#x([0-9a-f]+)|amp|lt|gt|quot|apos);/gi,
    (entity, decimal: string | undefined, hex: string | undefined) => {
      if (decimal) return decodeCodePoint(Number.parseInt(decimal, 10));
      if (hex) return decodeCodePoint(Number.parseInt(hex, 16));
      switch (entity.toLowerCase()) {
        case "&amp;":
          return "&";
        case "&lt;":
          return "<";
        case "&gt;":
          return ">";
        case "&quot;":
          return '"';
        case "&apos;":
          return "'";
        default:
          return entity;
      }
    },
  );
}

function decodeCodePoint(value: number): string {
  if (
    !Number.isInteger(value) ||
    value < 0 ||
    value > 0x10ffff ||
    (value >= 0xd800 && value <= 0xdfff)
  ) {
    return "\uFFFD";
  }
  return String.fromCodePoint(value);
}

// The search backend emits escaped text with optional <mark> delimiters.
// Convert that narrow interface to React data instead of trusting arbitrary
// upstream HTML.
export function parseSearchSnippet(snippet: string): SearchSnippetSegment[] {
  const segments: SearchSnippetSegment[] = [];
  let highlighted = false;
  for (const token of snippet.split(/(<mark>|<\/mark>)/i)) {
    if (/^<mark>$/i.test(token)) {
      highlighted = true;
      continue;
    }
    if (/^<\/mark>$/i.test(token)) {
      highlighted = false;
      continue;
    }
    if (token) {
      segments.push({ text: decodeEscapedText(token), highlighted });
    }
  }
  return segments;
}
