# Markdown Hub

A self-hosted, database-centric markdown publishing system. Notes live in PostgreSQL — documents, images, and comments — and are served as a web feed. Content gets in through a plain HTTP write API (built for agents and scripts), or via a one-shot import of an existing Obsidian vault. No runtime dependency on Obsidian or the filesystem.

## Architecture

```
┌─────────────────┐   one-shot import   ┌──────────────────┐
│  Obsidian Vault │ ──────────────────▶ │  Go API (10002)   │
│  (optional)     │   mdhub-go -import  │  PostgreSQL       │ ← documents / images / comments
└─────────────────┘                     │                   │    (single source of truth)
┌─────────────────┐   PUT markdown      │                   │
│  Agents/scripts │ ──────────────────▶ │                   │
└─────────────────┘                     └────────┬─────────┘
                                                 │ server-side fetch
                                        ┌────────▼─────────┐
                                        │  Next.js (10001)  │ ← Markdown feed + viewer
                                        └──────────────────┘
```

- **Database**: PostgreSQL — the single source of truth (documents with raw markdown, images as bytea, tags, backlinks, comment threads)
- **API backend**: Go binary — owns all DB access, document write API, in-memory CJK bigram search, LLM categorization, image and comment endpoints
- **Frontend**: Next.js 16 (App Router) — fetches everything from the Go API server-side, renders markdown, proxies images

## Quick Start

### 1. Database

```bash
createdb mdhub
psql -d mdhub -f go-backend/schema.sql   # create tables
```

### 2. API backend

```bash
cd go-backend
cp .env.example .env
# Edit .env: set MDHUB_PG
go build -tags nodynamic -o mdhub-go .

# Optional: one-shot import of an existing vault (documents + images + comments);
# pass the vault ROOT — slugs stay vault-relative, e.g. "_translations/notes/foo":
./mdhub-go -import "/path/to/Obsidian Vault"

./mdhub-go                     # → http://localhost:10002
```

### 3. Frontend

```bash
cp .env.example .env.local
# Edit .env.local if MDHUB_API_URL differs
npm install
npm run dev        # → http://localhost:10001/mdhub
```

### 4. Optional: local semantic search (CPU)

```bash
brew install ollama && brew services start ollama
ollama pull qwen3-embedding:0.6b
# In go-backend/.env: MDHUB_EMBED_URL="http://localhost:11434", restart the backend, then:
curl -X POST -H "X-MDHub-Edit-Token: $MDHUB_EDIT_TOKEN" \
  http://localhost:10002/api/reembed   # backfill existing notes
```

## Publishing a note

The primary way to publish is the write API — agents and scripts upload raw markdown directly, no filesystem involved:

```bash
# Create or update a note (body is the full raw markdown; slug is chosen by the caller):
curl -X PUT -H "X-MDHub-Edit-Token: $MDHUB_EDIT_TOKEN" \
  --data-binary @note.md http://localhost:10002/api/documents/translations/note

# Delete:
curl -X DELETE -H "X-MDHub-Edit-Token: $MDHUB_EDIT_TOKEN" \
  http://localhost:10002/api/documents/translations/note
```

A note is publicly visible when its frontmatter has `publish: true`:

```yaml
---
title: My Note
publish: true
tags: [foo, bar]
category: 菜谱/家常   # optional: pins the tree-sidebar location, wins over LLM
---
```

The server parses the frontmatter, derives the plain-text excerpt and word count, updates tags/backlinks, and (if an LLM key is configured) queues the note for tree categorization — all on the write, so reads stay cheap.

A note whose frontmatter has `type: fleeting` is a **spark**: it stays private (never listed, searched, categorized, or shown in the Universe) but is still embedded and fed into the collision engine — see Features below.

The one-shot vault import (`-import`, above) also works as a bulk-publish path: it is idempotent, so re-running it after editing notes in Obsidian upserts only what changed.

The home page lists all published notes, newest first. Each gets a URL at `/view/<slug>/`.

## API overview (Go backend, :10002)

| Endpoint | Description |
|---|---|
| `GET /api/documents` | Published notes (slug, title, excerpt, updated) |
| `GET /api/documents/{slug}` | One note incl. `raw_content`, tags, backlinks |
| `PUT /api/documents/{slug}` | Create/update from raw markdown body |
| `DELETE /api/documents/{slug}` | Delete note (tags/backlinks/comments cascade) |
| `GET /api/search?q=` | Full-text search with snippets (CJK bigram, in-memory) |
| `GET /api/universe` | Published-document nodes and sparse semantic-similarity edges |
| `GET /api/related?slug=` | Up to five semantic neighbours for a published document |
| `GET /api/tags` · `GET /api/tags?tag=` | Tag counts / notes per tag |
| `GET /api/backlinks/{slug}` | Notes linking to a slug |
| `POST /api/images` | Upload a ≤20 MB PNG/JPEG/GIF/WebP/AVIF image (`multipart/form-data`) |
| `GET /api/images?path=` | Image binary stored in PG |
| `GET/POST /api/documents/{slug}/comments` | Anchored comment threads |
| `POST /api/reindex` | Rebuild the in-memory search index from PG |
| `POST /api/reclassify` | Queue LLM classification for published notes with no category yet |
| `POST /api/reembed` | Queue embedding computation for all published notes |
| `GET /api/sparks` | Fleeting notes (sparks), newest first, with collision counts — edit token required |
| `GET /api/collisions` · `GET /api/collisions?slug=` | Recent collision pairs (joins both titles); anonymous callers only see pairs where both sides are published |
| `POST /api/collisions/{id}` | Set a pair's verdict: `confirmed` / `dismissed` / `new` |
| `POST /api/recollide` | Queue collision detection for every embedded document (backfill) |

## Environment Variables

### Frontend (`.env.local`)

| Variable | Default | Description |
|---|---|---|
| `MDHUB_API_URL` | `http://localhost:10002` | Go API base URL (server-side) |
| `MDHUB_PUBLIC_BASE_URL` | `http://localhost:10001/mdhub` | Public base URL |
| `NEXT_PUBLIC_SEARCH_API` | `http://localhost:10002` | Search backend URL (browser → Go) |
| `NEXT_PUBLIC_HEARTH_URL` | _(unset)_ | Optional "back to Hearth" link |

### Go Backend (`.env`)

| Variable | Default | Description |
|---|---|---|
| `MDHUB_PG` | `postgres://mdhub:***@localhost:5432/mdhub` | PostgreSQL DSN |
| `MDHUB_LISTEN` | `127.0.0.1:10002` | Listen address; expose deliberately only behind an authenticated proxy |
| `MDHUB_EDIT_TOKEN` | _(unset = loopback document writes only; image uploads disabled)_ | Shared secret for document/image/maintenance writes; mandatory for non-loopback listen addresses |
| `MDHUB_LLM_BASE_URL` | `https://api.openai.com/v1` | OpenAI-compatible chat API base |
| `MDHUB_LLM_API_KEY` | _(unset = disabled)_ | LLM API key |
| `MDHUB_LLM_MODEL` | `gpt-4o-mini` | LLM model for categorization |
| `MDHUB_EMBED_URL` | _(unset = disabled)_ | Embedding API base, e.g. `http://localhost:11434` (Ollama) |
| `MDHUB_EMBED_MODEL` | `qwen3-embedding:0.6b` | Embedding model for semantic search |

## Features

- **Database-first**: PostgreSQL is the source of truth. The vault filesystem is only read once, by the importer.
- **Frontmatter-driven**: `publish: true` controls visibility; `tags` enable filtering; `type: fleeting` marks a private spark.
- **Hybrid search**: In-memory CJK bigram keyword matching (PostgreSQL's tsvector silently drops CJK on macOS) blended with local embedding semantics — optionally powered by Qwen3-Embedding-0.6B via Ollama on CPU, so "怎么烹饪猪肉" can find a 红烧肉 note with no literal overlap. Disabled when `MDHUB_EMBED_URL` is unset.
- **Knowledge Universe**: A top-level semantic map beside Documents. Published notes are nodes; mutual embedding-neighbour relationships form a sparse graph, with one strongest-neighbour fallback for an otherwise isolated embedded note. Long notes are represented by pooling up to six evenly sampled chunks instead of only their introduction. The map supports zoom, pan, search, per-node edge density, and document-level inspection. Notes without embeddings remain visible as disconnected nodes until `POST /api/reembed` completes.
- **Images in PG**: Image binaries are imported or uploaded into the database and served through `/api/images`. Uploads use reserved SHA-256 content-addressed paths; large static images are resized and converted to WebP in a time-limited worker before storage.
- **Anchored comments**: Readers select text to comment on; threads are stored in PG and shown beside the article.
- **Tree sidebar**: The home page has a filesystem-style sidebar — drill down level by level, with a breadcrumb bar on top.
- **LLM semantic categories**: Optionally, an OpenAI-compatible LLM organizes published notes into a category tree that drives the sidebar. The algorithm works like a B-tree: each note descends from the root into the best-fitting folder, and any node with more than 10 direct children is split — the LLM reads the full text of that node's notes and proposes named groups. All work is local to the affected node (no global recomputation), async on write only, never on the read path; degrades to plain directory grouping when no API key is set. Frontmatter `category:` pins a note manually (never moved by splits) and always wins. Rebuild the whole tree with `POST /api/reclassify`.
- **Sparks & collision engine**: Fleeting captures (`type: fleeting`) skip the feed, search, categories, and Universe, but still get embedded. After each embed, a background worker compares the fresh vector against the whole library (cosine ≥ 0.55, top 5) and, when an LLM key is configured, writes a non-obvious connection plus one open question per new pair. The Sparks page offers quick capture, a collision stream for confirming or dismissing pairs, and an inspiration stream that flags stranded sparks (no collisions after 7 days). Sparks never leak to anonymous readers: `/api/sparks` requires the edit token, and `/api/collisions` only shows anonymous callers pairs where both sides are published. Growing up is manual: rewrite a spark into a note with `publish: true`.
- **Font presets**: 6 Chinese font options (system, serif, kai, hei, wenkai, fangsong).
- **⌘K search**: Fuzzy full-text search with inline snippets.

## Migrating from the filesystem version

If you ran MDHub when it read the vault directly:

1. Apply the new schema (note: it is **not** compatible with the v1 tables — recreate the database or `DROP TABLE documents, tags, document_tags, backlinks` first).
2. Run the import: `./mdhub-go -import "/path/to/Obsidian Vault"` — this imports all notes (published flag preserved), image files, and `_comments/*.md` threads. The import is idempotent; re-running overwrites in place.
3. Remove `MDHUB_VAULT_PATH` / `MDHUB_VAULT` from your env files.

## Production deploy

```bash
npm run build
npm start            # Next.js on :10001
# In go-backend/:
go build -tags nodynamic -o mdhub-go . && ./run.sh   # Go API on :10002
```

Launchd plist examples for macOS are available — see `scripts/start.sh` and `go-backend/run.sh` for the entry points.

## License

MIT
