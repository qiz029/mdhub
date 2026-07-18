# Markdown Hub V2 — Vault Publish Rebuild

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task.

**Goal:** Rebuild mdhub from a token-based Pastebin into a family Obsidian Publish — scans vault filesystem for `publish: true` frontmatter, renders a chronological feed, supports local image serving, all from a clean stone-minimal UI.

**Architecture:** Vault is the single source of truth. mdhub reads markdown files directly from the filesystem (Syncthing synced vault on the Mac mini). Frontmatter YAML in each `.md` file controls visibility. No SQLite. No tokens. No publish API. `/api/image` simplified to vault-scoped authorization.

**Tech Stack:** Next.js 16, Tailwind CSS 4, marked v18, lucide-react, gray-matter (new dep for frontmatter), fs/promises.

**Design Language (preserved from V1):** Stone palette, `tracking-tight` headings, `tracking-widest` section labels, `prose-md` custom markdown CSS, `border-b border-stone-200` top nav with Flame icon → Hearth, no shadows, no fluorescent colors.

---

### Task 1: Add gray-matter dependency

**Objective:** Install the frontmatter parsing library.

**Files:**
- Modify: `package.json` (add dependency)

**Step 1: Install gray-matter**

Run:
```bash
cd ~/workspace/mdhub && npm install gray-matter
```

**Step 2: Verify**
```bash
node -e "const matter = require('gray-matter'); console.log(matter('---\ntitle: Hi\n---\nHello').data.title)"
```
Expected: "Hi"

---

### Task 2: Rewrite config.ts for vault path

**Objective:** Replace old `MDHUB_ROOT_DIR` (cron output dir) with `VAULT_PATH` pointing at the Obsidian vault.

**Files:**
- Modify: `src/lib/config.ts`

**Complete replacement:**

```ts
import { homedir } from "node:os";
import path from "node:path";

export const VAULT_PATH =
  process.env.MDHUB_VAULT_PATH ||
  path.join(homedir(), "Obsidian", "Vault");

export const PUBLIC_BASE_URL =
  process.env.MDHUB_PUBLIC_BASE_URL ||
  "http://todds-mac-mini.local/mdhub";
```

**Verification:** File saves without syntax errors.

---

### Task 3: Create vault.ts — scan + frontmatter parser

**Objective:** Create `src/lib/vault.ts` with functions to scan the vault for published markdown files.

**Files:**
- Create: `src/lib/vault.ts`

**Step 1: Create the file**

```ts
import { readFile, readdir, stat } from "node:fs/promises";
import path from "node:path";
import matter from "gray-matter";
import { VAULT_PATH } from "./config";

export type PublishedEntry = {
  slug: string;           // relative path without .md, e.g. "_agent/weekly-report"
  filePath: string;       // absolute path
  title: string;          // from frontmatter title or first # heading
  source: "human" | "agent";  // from frontmatter, default "human"
  publishedAt: number;    // mtimeMs
  excerpt: string;        // first 200 chars of body
};

async function* walkDir(dir: string): AsyncGenerator<string> {
  const entries = await readdir(dir, { withFileTypes: true });
  for (const entry of entries) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      // Skip hidden dirs and node_modules
      if (entry.name.startsWith(".") || entry.name === "node_modules") continue;
      yield* walkDir(full);
    } else if (entry.name.endsWith(".md")) {
      yield full;
    }
  }
}

export async function listPublished(): Promise<PublishedEntry[]> {
  const results: PublishedEntry[] = [];

  try {
    for await (const filePath of walkDir(VAULT_PATH)) {
      try {
        const raw = await readFile(filePath, "utf8");
        const { data, content } = matter(raw);

        // Only include files with publish: true
        if (data.publish !== true) continue;

        const fileStat = await stat(filePath);
        const relPath = path.relative(VAULT_PATH, filePath);
        const slug = relPath.replace(/\\/g, "/").replace(/\.md$/, "");

        // Title: frontmatter title > first # heading > slug
        let title = data.title as string | undefined;
        if (!title) {
          const match = content.match(/^\s*#\s+(.+?)\s*$/m);
          title = match ? match[1].trim() : slug;
        }

        // Source: agent if in _agent/ dir, otherwise human
        const source = slug.startsWith("_agent/") ? "agent" : "human";

        // Excerpt: first 200 non-empty chars after frontmatter
        const excerpt = content.replace(/\s+/g, " ").trim().slice(0, 200);

        results.push({
          slug,
          filePath,
          title,
          source,
          publishedAt: fileStat.mtimeMs,
          excerpt,
        });
      } catch {
        // Skip files that can't be read (permissions, binary, etc.)
      }
    }
  } catch {
    // Vault directory doesn't exist or can't be read — return empty
  }

  // Sort by publishedAt descending (newest first)
  results.sort((a, b) => b.publishedAt - a.publishedAt);
  return results;
}

export async function getPublishedFile(
  slug: string,
): Promise<{ filePath: string; content: string } | null> {
  const cleanSlug = slug.replace(/\.\./g, "").replace(/\\/g, "/");
  const filePath = path.resolve(VAULT_PATH, `${cleanSlug}.md`);

  // Security: ensure the resolved path is still inside the vault
  if (!filePath.startsWith(VAULT_PATH + path.sep) && filePath !== VAULT_PATH) {
    return null;
  }

  try {
    const raw = await readFile(filePath, "utf8");
    const { data } = matter(raw);

    if (data.publish !== true) return null;

    return { filePath, content: raw };
  } catch {
    return null;
  }
}
```

**Verification:** File saves without syntax errors.

---

### Task 4: Rewrite home page as publish feed

**Objective:** Replace `src/app/page.tsx` (currently a redirect to /admin) with the publish feed.

**Files:**
- Modify: `src/app/page.tsx`

**Complete replacement:**

```tsx
import { listPublished, type PublishedEntry } from "@/lib/vault";
import Link from "next/link";
import { Nav } from "@/components/Nav";

export const dynamic = "force-dynamic";

function fmtDate(ms: number): string {
  return new Date(ms).toLocaleString("zh-CN", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function Card({ entry }: { entry: PublishedEntry }) {
  return (
    <Link
      href={`/view/${entry.slug}`}
      className="block group"
    >
      <article className="border-b border-stone-100 py-6 transition-colors hover:bg-stone-50/50 -mx-3 px-3 rounded-lg">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2 mb-1.5">
              <span className="text-sm font-semibold tracking-tight text-stone-900 group-hover:text-stone-700">
                {entry.title}
              </span>
              <span
                className={`shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium ${
                  entry.source === "agent"
                    ? "bg-amber-100 text-amber-700"
                    : "bg-stone-100 text-stone-500"
                }`}
              >
                {entry.source === "agent" ? "Agent" : "Todd"}
              </span>
            </div>
            <p className="text-sm text-stone-500 line-clamp-2">
              {entry.excerpt || "No preview"}
            </p>
          </div>
          <time className="shrink-0 text-xs text-stone-400 mt-0.5">
            {fmtDate(entry.publishedAt)}
          </time>
        </div>
      </article>
    </Link>
  );
}

export default async function HomePage() {
  const entries = await listPublished();

  return (
    <div>
      <Nav />
      <main className="mx-auto max-w-3xl px-6 py-10 space-y-8">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-stone-900">
            Published
          </h1>
          <p className="mt-1 text-sm text-stone-400">
            Notes and reports from the family vault
          </p>
        </div>

        {entries.length === 0 ? (
          <div className="py-24 text-center">
            <p className="text-sm text-stone-400">
              Nothing published yet.
            </p>
            <p className="mt-1 text-xs text-stone-300">
              Add <code className="text-stone-400">publish: true</code> to a
              note&apos;s frontmatter to see it here.
            </p>
          </div>
        ) : (
          <div>
            {entries.map((entry) => (
              <Card key={entry.slug} entry={entry} />
            ))}
          </div>
        )}
      </main>
    </div>
  );
}
```

**Verification:** File saves without syntax errors.

---

### Task 5: Rewrite view page as slug-based

**Objective:** Replace token-based `/view/[token]` with slug-based `/view/[slug]`. Remove token/auth logic.

**Files:**
- Delete: `src/app/view/[token]/page.tsx`, `src/app/view/[token]/ViewerActions.tsx`
- Create: `src/app/view/[slug]/page.tsx`
- Create: `src/components/ViewerActions.tsx`
- Modify: `src/lib/markdown.ts` — move `extractTitle` export to a shared location (already exported), add frontmatter-stripping before render

**Step 1: Delete old files**

```bash
rm -rf ~/workspace/mdhub/src/app/view/\[token\]
```

**Step 2: Move ViewerActions to shared component**

```bash
mkdir -p ~/workspace/mdhub/src/components
```

Create `src/components/ViewerActions.tsx` (same content as old `ViewerActions.tsx` but remove token prop):

```tsx
"use client";

import { useState } from "react";

export function ViewerActions({
  markdown,
  downloadName,
}: {
  markdown: string;
  downloadName: string;
}) {
  const [copied, setCopied] = useState(false);

  async function copyMarkdown() {
    try {
      await navigator.clipboard.writeText(markdown);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      const ta = document.createElement("textarea");
      ta.value = markdown;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand("copy");
      document.body.removeChild(ta);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }
  }

  function downloadMarkdown() {
    const blob = new Blob([markdown], { type: "text/markdown" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = downloadName;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }

  return (
    <div className="flex shrink-0 gap-2">
      <button
        type="button"
        onClick={copyMarkdown}
        className="rounded-md border border-stone-300 bg-white px-3 py-1.5 text-xs font-medium text-stone-700 hover:bg-stone-50"
      >
        {copied ? "Copied!" : "Copy"}
      </button>
      <button
        type="button"
        onClick={downloadMarkdown}
        className="rounded-md border border-stone-300 bg-white px-3 py-1.5 text-xs font-medium text-stone-700 hover:bg-stone-50"
      >
        Download
      </button>
    </div>
  );
}
```

**Step 3: Update markdown.ts to add a content-only extractor for rendering**

Add to `src/lib/markdown.ts` (patch — add after existing `extractTitle` export):

```ts
export function stripFrontmatter(md: string): string {
  // gray-matter would work here, but we avoid the dep in this utility
  // Simple approach: if starts with ---, find closing ---
  if (md.startsWith("---")) {
    const second = md.indexOf("---", 3);
    if (second !== -1) {
      return md.slice(second + 3).trimStart();
    }
  }
  return md;
}
```

Update `renderMarkdown` to strip frontmatter before rendering? No — `marked` handles `---` well enough since it's not standard markdown and gets treated as a horizontal rule. Actually, let's just strip it because `---` will render as `<hr>` which looks wrong at the top of a published document.

Patch `src/lib/markdown.ts`: add import of `stripFrontmatter` and use it in render:

Actually, let me simplify: just update `renderMarkdown` to strip frontmatter internally.

**Updated renderMarkdown in `src/lib/markdown.ts`:**

```ts
import { marked } from "marked";
import path from "node:path";

export function extractTitle(md: string, fallback: string): string {
  const match = md.match(/^\s*#\s+(.+?)\s*$/m);
  if (match) return match[1].trim();
  return fallback;
}

function stripFrontmatter(md: string): string {
  if (md.startsWith("---")) {
    const second = md.indexOf("---", 3);
    if (second !== -1) return md.slice(second + 3).trimStart();
  }
  return md;
}

function isAbsoluteUrl(href: string): boolean {
  return /^([a-z][a-z0-9+.-]*:)?\/\//i.test(href) || href.startsWith("data:");
}

export async function renderMarkdown(
  md: string,
  fileDir: string,
): Promise<string> {
  const body = stripFrontmatter(md);
  const renderer = new marked.Renderer();

  const origImage = renderer.image.bind(renderer);
  renderer.image = ({ href, title, text }) => {
    if (!href) return origImage({ href: "", title, text });
    if (isAbsoluteUrl(href) || href.startsWith("/api/image")) {
      return origImage({ href, title, text });
    }
    const abs = path.isAbsolute(href) ? href : path.resolve(fileDir, href);
    const url = `/api/image?path=${encodeURIComponent(abs)}`;
    return origImage({ href: url, title, text });
  };

  marked.setOptions({ gfm: true, breaks: false });
  return await marked.parse(body, { renderer, async: true });
}
```

**Step 4: Create new view page**

- Modify: `src/app/view/[...slug]/page.tsx`

**Step 4: Create new view page**

Create `src/app/view/[...slug]/page.tsx`:

```tsx
import path from "node:path";
import { notFound } from "next/navigation";
import { Nav } from "@/components/Nav";
import { ViewerActions } from "@/components/ViewerActions";
import { getPublishedFile } from "@/lib/vault";
import { extractTitle, renderMarkdown } from "@/lib/markdown";

export const dynamic = "force-dynamic";

export default async function ViewPage({
  params,
}: {
  params: Promise<{ slug: string[] }>;
}) {
  const { slug: segments } = await params;
  const slug = segments.join("/");
  const file = await getPublishedFile(slug);

  if (!file) {
    return (
      <div>
        <Nav />
        <main className="mx-auto max-w-2xl px-6 py-24 text-center">
          <p className="text-xs uppercase tracking-widest text-stone-400">
            404
          </p>
          <h1 className="mt-3 text-2xl font-semibold text-stone-900">
            Not found or not published
          </h1>
          <p className="mt-3 text-stone-600">
            This page doesn&apos;t exist or its frontmatter doesn&apos;t have{" "}
            <code className="text-stone-400">publish: true</code>.
          </p>
        </main>
      </div>
    );
  }

  const fileDir = path.dirname(file.filePath);
  const baseName = path.basename(slug);
  const title = extractTitle(file.content, baseName);
  const html = await renderMarkdown(file.content, fileDir);
  const downloadName = path.basename(file.filePath);

  return (
    <div>
      <Nav />
      <main className="mx-auto w-full max-w-[65ch] px-6 py-10">
        <div className="mb-6 flex items-start justify-between gap-4">
          <h1 className="text-3xl font-bold tracking-tight text-stone-900">
            {title}
          </h1>
          <ViewerActions
            markdown={file.content}
            downloadName={downloadName}
          />
        </div>
        <article
          className="prose-md text-stone-800"
          dangerouslySetInnerHTML={{ __html: html }}
        />
      </main>
    </div>
  );
}
```

**Verification:** File saves without syntax errors.

---

### Task 6: Update Nav component

**Objective:** Remove Admin link, update viewer nav to include Home.

**Files:**
- Modify: `src/components/Nav.tsx`

**Complete replacement:**

```tsx
import Link from "next/link";
import { Flame } from "lucide-react";

export function Nav() {
  return (
    <header className="border-b border-stone-200 bg-white">
      <div className="mx-auto flex max-w-5xl items-center justify-between px-6 py-3">
        <div className="flex items-center gap-4">
          <a
            href="http://todds-mac-mini.local"
            className="flex items-center gap-1.5 text-xs text-stone-400 hover:text-stone-700 transition-colors"
            title="Back to Hearth"
          >
            <Flame size={14} className="text-amber-400" />
            <span>Hearth</span>
          </a>
          <span className="text-stone-200">|</span>
          <Link
            href="/"
            className="text-sm font-semibold tracking-tight text-stone-900 hover:text-stone-700"
          >
            Markdown Hub
          </Link>
        </div>
        <nav className="flex items-center gap-4 text-sm text-stone-600">
          <Link href="/" className="hover:text-stone-900">
            Home
          </Link>
        </nav>
      </div>
    </header>
  );
}
```

**Verification:** File saves without syntax errors.

---

### Task 7: Simplify image API (remove token auth)

**Objective:** Remove SQLite-based token authorization from image API. Now any image inside the vault is authorized.

**Files:**
- Modify: `src/app/api/image/route.ts`

**Complete replacement:**

```ts
import { NextRequest } from "next/server";
import { readFile, stat } from "node:fs/promises";
import path from "node:path";
import { VAULT_PATH } from "@/lib/config";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const MIME: Record<string, string> = {
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".gif": "image/gif",
  ".webp": "image/webp",
  ".svg": "image/svg+xml",
  ".bmp": "image/bmp",
  ".avif": "image/avif",
  ".ico": "image/x-icon",
};

export async function GET(req: NextRequest) {
  const url = new URL(req.url);
  const raw = url.searchParams.get("path");
  if (!raw) return new Response("missing path", { status: 400 });

  const abs = path.resolve(raw);
  const ext = path.extname(abs).toLowerCase();
  if (!MIME[ext]) {
    return new Response("unsupported type", { status: 400 });
  }

  // Security: only serve images inside the vault
  if (
    !abs.startsWith(VAULT_PATH + path.sep) &&
    abs !== VAULT_PATH
  ) {
    return new Response("forbidden", { status: 403 });
  }

  try {
    const s = await stat(abs);
    if (!s.isFile()) return new Response("not a file", { status: 404 });
    const buf = await readFile(abs);
    return new Response(buf, {
      status: 200,
      headers: {
        "Content-Type": MIME[ext],
        "Cache-Control": "private, max-age=300",
        "Content-Length": String(buf.byteLength),
      },
    });
  } catch {
    return new Response("not found", { status: 404 });
  }
}
```

**Verification:** File saves without syntax errors.

---

### Task 8: Delete old API routes and unused code

**Objective:** Remove all token/publish/file tracking APIs and the old admin page.

**Files:**
- Delete: `src/app/api/tokens/route.ts`
- Delete: `src/app/api/tokens/[id]/route.ts`
- Delete: `src/app/api/publish/route.ts`
- Delete: `src/app/api/files/route.ts`
- Delete: `src/app/admin/page.tsx`
- Delete: `src/lib/db.ts`

**Commands:**

```bash
rm ~/workspace/mdhub/src/app/api/tokens/route.ts
rm -rf ~/workspace/mdhub/src/app/api/tokens/\[id\]
rm ~/workspace/mdhub/src/app/api/publish/route.ts
rm ~/workspace/mdhub/src/app/api/files/route.ts
rm -rf ~/workspace/mdhub/src/app/admin
rm ~/workspace/mdhub/src/lib/db.ts
```

Clean up empty directories:

```bash
rmdir ~/workspace/mdhub/src/app/api/tokens 2>/dev/null || true
rmdir ~/workspace/mdhub/src/app/api/publish 2>/dev/null || true
rmdir ~/workspace/mdhub/src/app/api/files 2>/dev/null || true
```

**Verification:**
```bash
ls ~/workspace/mdhub/src/app/api/
```
Expected output: only `image/` directory exists.

---

### Task 9: Update config.ts for PUBLIC_BASE_URL (optional, clarify)

**Objective:** Ensure PUBLIC_BASE_URL is correct for the new viewer URL pattern.

**Files:**
- Verify: `src/lib/config.ts`

**Check:** The URL pattern changes from `/view/[token]` to `/view/[slug]`. No code changes needed since `PUBLIC_BASE_URL` is just a host prefix. But verify the constant is correct:

```ts
export const PUBLIC_BASE_URL =
  process.env.MDHUB_PUBLIC_BASE_URL ||
  "http://todds-mac-mini.local/mdhub";
```

This is correct. Links in the feed are relative (`/view/[slug]`), so PUBLIC_BASE_URL is only used if we ever need absolute URLs.

---

### Task 10: Build verification

**Objective:** Ensure the project compiles successfully.

**Command:**
```bash
cd ~/workspace/mdhub && npx next build 2>&1
```

**Expected:** Build succeeds with no TypeScript errors.

**Troubleshooting:**
- If `gray-matter` types are missing: `npm install --save-dev @types/node` (should already exist)
- If `cannot find module '@/lib/vault'`: verify `tsconfig.json` has `"paths": { "@/*": ["./src/*"] }`
- If unused import warnings: clean up imports

---

### Task 11: Test with sample vault data

**Objective:** Create a test markdown file in a mock vault and verify the feed renders.

**Step 1: Create test vault directory**

```bash
mkdir -p /tmp/test-vault/_agent
```

**Step 2: Create a published note**

```bash
cat > /tmp/test-vault/hello.md << 'EOF'
---
publish: true
title: Hello from the Vault
---

# Hello from the Vault

This is a test note. It should appear on the mdhub home page.

## Section

Some content here.

![test](https://placehold.co/100x100/png)
EOF
```

**Step 3: Create an agent note**

```bash
cat > /tmp/test-vault/_agent/weekly-report.md << 'EOF'
---
publish: true
---

# Weekly Report

The agent generated this report. It should show up with an "Agent" badge.

- Item 1
- Item 2
- Item 3
EOF
```

**Step 4: Create an unpublished note (should NOT appear)**

```bash
cat > /tmp/test-vault/draft.md << 'EOF'
# Secret Draft

This should not appear on the feed because there's no publish: true.
EOF
```

**Step 5: Run dev server with test vault**

```bash
cd ~/workspace/mdhub && MDHUB_VAULT_PATH=/tmp/test-vault npm run dev &
sleep 3
```

**Step 6: Verify home page**

```bash
curl -s http://localhost:10001/mdhub/ | grep -o "Hello from the Vault"
curl -s http://localhost:10001/mdhub/ | grep -o "Weekly Report"
curl -s http://localhost:10001/mdhub/ | grep -o "Secret Draft"
```

Expected: "Hello from the Vault" found, "Weekly Report" found, "Secret Draft" NOT found.

**Step 7: Verify viewer page**

```bash
curl -s http://localhost:10001/mdhub/view/hello | head -20
curl -s http://localhost:10001/mdhub/view/draft
```

Expected: `/view/hello` shows content, `/view/draft` returns 404 or "not published" message.

**Step 8: Kill dev server**

```bash
kill %1 2>/dev/null || true
```

---

### Decisions Summary

| # | Decision | Conclusion |
|---|----------|------------|
| 1 | Product positioning | Family Obsidian Publish (B — agent feed + human curated) |
| 2 | MD interaction mode | Agent reads your notes, writes to `_agent/`, mdhub displays (C) |
| 3 | Vault sync | Syncthing |
| 4 | Agent write space | Same vault, `_agent/` subdirectory |
| 5 | mdhub role | Family Obsidian Publish, iPad-hosted display |
| 6 | Publish mechanism | Frontmatter `publish: true` (both human and agent can set) |
| 7 | Data source | Direct filesystem read from vault (no SQLite, no publish API) |
| 8 | Pages | `/` feed, `/view/[slug]` single article |
| 9 | Design style | Preserve V1 stone-minimal aesthetic |
```
