import assert from "node:assert/strict";
import test from "node:test";

import { renderMarkdown } from "./markdown.ts";

test("renderMarkdown escapes raw HTML", async () => {
  const { html } = await renderMarkdown(
    '<script>alert(1)</script>\n<img src=x onerror="alert(2)">',
    "",
  );

  assert.doesNotMatch(html, /<script|<img/i);
  assert.match(html, /&lt;script&gt;/);
});

test("renderMarkdown only strips a complete frontmatter block", async () => {
  const complete = await renderMarkdown(
    "---\ntitle: Note\n---\nVisible",
    "",
  );
  assert.doesNotMatch(complete.html, /title: Note/);
  assert.match(complete.html, /Visible/);

  const incomplete = await renderMarkdown(
    "---\nVisible without a closing delimiter",
    "",
  );
  assert.match(incomplete.html, /Visible without a closing delimiter/);
});

test("renderMarkdown blocks active link protocols after browser normalization", async () => {
  const payloads = [
    "javascript:alert(1)",
    "JaVaScRiPt:alert(1)",
    "&#x6a;avascript:alert(1)",
    "java&Tab;script:alert(1)",
    "data:text/html;base64,PHNjcmlwdD4=",
    "vbscript:msgbox(1)",
  ];

  for (const href of payloads) {
    const { html } = await renderMarkdown(`[unsafe](${href})`, "");
    assert.doesNotMatch(html, /<a\b/i, href);
  }

  const { html } = await renderMarkdown(
    "[web](https://example.com) [mail](mailto:test@example.com)",
    "",
  );
  assert.match(html, /href="https:\/\/example\.com"/);
  assert.match(html, /href="mailto:test@example\.com"/);
});

test("renderMarkdown only permits passive raster data images", async () => {
  const { html } = await renderMarkdown(
    "![bad](javascript:alert(1)) ![svg](data:image/svg+xml;base64,PHN2Zz4=) ![png](data:image/png;base64,iVBORw0KGgo=)",
    "",
  );

  assert.doesNotMatch(html, /javascript:|image\/svg\+xml/i);
  assert.match(html, /src="data:image\/png;base64,iVBORw0KGgo="/);
});

test("renderMarkdown keeps resolved and unresolved wiki links inert", async () => {
  const resolve = (target: string) =>
    target === "Known" ? "notes/known" : null;
  const { html } = await renderMarkdown(
    "[[Known|safe]] [[Missing|dead]] [unsafe](javascript:alert(1))",
    "",
    resolve,
  );

  assert.match(html, /href="\/mdhub\/view\/notes\/known"/);
  assert.match(html, /class="mdhub-dead-link"/);
  assert.doesNotMatch(html, /javascript:/i);
});
