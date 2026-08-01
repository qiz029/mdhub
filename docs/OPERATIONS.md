# MDHub Operations Manual

Audience: agents and operators deploying and running MDHub. Every procedure
below is copy-paste executable; verification commands include their expected
output shape. README.md covers product overview — this file covers running it.

## 1. System topology

| Component | Default port | Started by | Required |
|---|---|---|---|
| PostgreSQL | 5432 | system (brew services etc.) | yes |
| Go API (`go-backend/mdhub-go`) | 10002 | `go-backend/run.sh` | yes |
| Next.js frontend | 10001 | `scripts/start.sh` | yes |
| Ollama (embeddings) | 11434 | `brew services start ollama` | optional (semantic search) |
| LLM chat API (any OpenAI-compatible) | remote | — | optional (tree categorization) |

Data flows: agents/scripts → PUT markdown → Go API → PostgreSQL → Next.js reads
via Go API. There is no filesystem dependency at runtime.

## 2. First-time deployment

```bash
# --- 2.1 Database ---
createdb mdhub
psql -d mdhub -f go-backend/schema.sql        # idempotent; safe to re-run

# --- 2.2 Go backend ---
cd go-backend
cp .env.example .env                           # edit: MDHUB_PG at minimum
go build -tags nodynamic -o mdhub-go .              # deterministic WASM WebP codec

# Optional: one-shot import of an existing Obsidian vault (vault ROOT):
./mdhub-go -import "/path/to/Obsidian Vault"   # idempotent; safe to re-run

./run.sh                                       # serves on :10002

# --- 2.3 Frontend ---
cd ..
cp .env.example .env.local                     # edit: MDHUB_API_URL if not default
npm install
npm run build
./scripts/start.sh                             # serves on :10001, base path /mdhub
```

### 2.4 Optional: LLM tree categorization

Set in `go-backend/.env`, restart backend:

```
MDHUB_LLM_BASE_URL="https://api.openai.com/v1"   # any OpenAI-compatible endpoint
MDHUB_LLM_API_KEY="sk-..."
MDHUB_LLM_MODEL="gpt-4o-mini"
```

Backfill existing notes: `curl -X POST http://localhost:10002/api/reclassify`

### 2.5 Optional: local semantic search (CPU)

```bash
brew install ollama
brew services start ollama
ollama pull qwen3-embedding:0.6b                 # 639 MB
# go-backend/.env: MDHUB_EMBED_URL="http://localhost:11434", restart backend
curl -X POST http://localhost:10002/api/reembed  # backfill existing notes
```

### 2.6 Post-deploy verification

```bash
curl -s http://localhost:10002/health                       # → ok
curl -s http://localhost:10002/api/documents | head -c 200  # → JSON array (possibly [])
curl -s http://localhost:10002/api/universe | head -c 200   # → nodes / semantic edges / coverage metadata
curl -s "http://localhost:10002/api/search?q=test"          # → JSON array
curl -s http://localhost:10001/mdhub/ | grep -o '<title>[^<]*' # → <title>Markdown Hub
psql -d mdhub -c '\dt'   # documents, tags, document_tags, backlinks,
                         # images, comment_threads, comment_entries, embeddings
```

## 3. Publishing notes (primary agent workflow)

### 3.1 Create or update

```bash
curl -X PUT --data-binary @note.md http://localhost:10002/api/documents/<slug>
```

- Body is the **complete raw markdown**, frontmatter included. `Content-Type` is irrelevant; the raw body is used as-is.
- `<slug>` is caller-chosen, may contain `/` (e.g. `reports/2026-07-weekly`). It becomes the note's public URL: `/mdhub/view/<slug>/`.
- PUT is an upsert: same slug = full replace.

Frontmatter contract (parsed by the server):

```yaml
---
title: My Note          # optional; falls back to first "# " heading, then slug
publish: true           # required for public visibility; anything else = hidden draft
tags: [foo, bar]        # optional
category: 菜谱/家常      # optional; pins tree-sidebar location, exempt from LLM moves
source: agent           # optional free-form provenance string
---
```

On every published PUT the server: re-derives excerpt/word count, rewrites
tags and backlinks, updates the search index, and (if enabled) queues LLM
categorization and embedding — all async except the DB write.

Response: `{"status":"ok"}` (200). Verify visibility:
`curl -s http://localhost:10002/api/documents/<slug>` → JSON with `"published":true`.

### 3.2 Delete

```bash
curl -X DELETE http://localhost:10002/api/documents/<slug>   # → {"status":"ok"}
```

Tags, backlinks, comments, and embeddings cascade automatically.

### 3.3 Images

The document editor accepts selected, dropped, or pasted images. PNG, JPEG,
and WebP inputs larger than 5 MB or 2560 px on their longest side are resized
in the browser and encoded as WebP at approximately 82% quality. GIF and AVIF
are preserved. The original upload must be at most 20 MB.

Browser editing is disabled until the same `MDHUB_EDIT_TOKEN` is configured in
both `.env.local` and `go-backend/.env`. The editor asks for this token and
keeps it only in the current browser session. The Go image-upload endpoint also
requires the token, including for direct script calls.

Scripts can call the same backend interface directly:

```bash
curl -X POST http://localhost:10002/api/images \
  -H "X-MDHub-Edit-Token: $MDHUB_EDIT_TOKEN" \
  -F 'file=@/tmp/diagram.png'
# {"path":"uploads/ab/ab...png","href":"/uploads/ab/ab...png",...}
```

The server validates file signatures and decodable dimensions, rejects SVG and
non-image data, and enforces the same resize/WebP policy even when the browser
is bypassed. Transcoding runs in a single isolated worker process with a 30s
deadline. It then derives a SHA-256 content-addressed path and stores the binary in PostgreSQL.
Repeated uploads of identical processed content reuse the same row. Insert the
returned root-relative href into Markdown, for example
`![diagram](/uploads/ab/ab...png)`.

`uploads/` is reserved for immutable content-addressed uploads. The vault
importer deliberately skips images under that directory; use another vault
path for ordinary imported assets.

Imported vault images continue to use their existing vault-relative keys.

### 3.4 Comments

```bash
# Read threads:
curl -s http://localhost:10002/api/documents/<slug>/comments

# New anchored thread:
curl -X POST http://localhost:10002/api/documents/<slug>/comments \
  -H 'Content-Type: application/json' \
  -d '{"author":"AgentName","text":"...","anchor":{"quote":"原文片段","prefix":"前文","suffix":"后文"}}'

# Reply to a thread:
curl -X POST ... -d '{"author":"AgentName","text":"...","reply":"<threadId>"}'
```

Constraints: note must exist and be published (404 otherwise); `text` ≤ 2000
runes; new threads require `anchor.quote` (400 otherwise); author defaults to
`用户`, truncated to 30 runes.

## 4. Routine operations

### 4.1 Restart services

```bash
# Go backend (find and kill, then restart via run.sh)
pkill -f 'mdhub-go$|./mdhub-go' ; cd go-backend && mkdir -p logs && nohup ./run.sh > logs/api.log 2>&1 &

# Frontend — CAUTION: the server process is named "next-server", NOT "next start".
# `pkill -f "next start"` does NOT kill it and the stale process keeps the port
# (EADDRINUSE on restart, silently serving the old build). Use:
pkill -f next-server ; npm run build && nohup ./scripts/start.sh > /tmp/mdhub-web.log 2>&1 &

# Ollama
brew services restart ollama
```

### 4.2 Upgrade procedure

```bash
git pull
psql -d mdhub -f go-backend/schema.sql    # always re-run; it is idempotent (IF NOT EXISTS / ALTER IF NOT EXISTS)
cd go-backend && go build -tags nodynamic -o mdhub-go . && go test -count=1 ./...
cd .. && npm run build
# restart both services (4.1), then run the 2.6 verification block
```

### 4.3 Backup

```bash
pg_dump -Fc mdhub > mdhub-$(date +%Y%m%d).dump    # includes image bytea
# Restore:
createdb mdhub_restore && pg_restore -d mdhub_restore mdhub-YYYYMMDD.dump
```

Everything is in PostgreSQL; there is nothing else to back up.

### 4.4 Rebuilding derived data

All of these are idempotent and safe to run any time:

| Task | Command |
|---|---|
| Rebuild in-memory search index | `curl -X POST localhost:10002/api/reindex` |
| Rebuild LLM category tree (keeps pinned) | `curl -X POST localhost:10002/api/reclassify` |
| Recompute embeddings | `curl -X POST localhost:10002/api/reembed` |
| Re-sync from a vault | `./mdhub-go -import "<vault root>"` |

Run `POST /api/reembed` once after upgrading to the Knowledge Universe build so
existing documents use the whole-document sampled embedding representation.

## 5. Failure modes

| Symptom | Likely cause | Fix |
|---|---|---|
| Frontend shows old content after restart | stale `next-server` process kept port 10001 | `pkill -f next-server`, start again (see 4.1) |
| Search returns nothing, DB has docs | in-memory index empty/stale | `POST /api/reindex`; check backend log for `index loaded, N published docs` |
| Chinese search empty | — | expected to work (in-memory bigram); if broken it is a bug, not a tsvector issue |
| Semantic search silently keyword-only | `MDHUB_EMBED_URL` unset, or Ollama down | backend log prints `embedding semantic search disabled`; `curl localhost:11434/api/version`; `POST /api/reembed` after fix |
| Sidebar shows directory tree, no semantic folders | no LLM key, or notes unclassified | set `MDHUB_LLM_API_KEY`, `POST /api/reclassify`; check log for `classified ... -> ...` / `split ...` |
| Sidebar folder >10 entries | all notes in it are pinned (`category:` frontmatter) | expected; splits never move pinned notes |
| Image 404 in a note | key mismatch or never imported | key = slug dir + relative ref; check `SELECT path FROM images` |
| Comments 400 | missing `anchor.quote` on new thread, or text >2000 | see 3.4 constraints |
| `-import` skipped comments (`skip comments for unknown doc`) | `note:` frontmatter slug ≠ document slug | slugs are vault-root-relative, e.g. `_translations/notes/foo` |
| `classify`/`embed` errors with 30s/120s timeouts | LLM endpoint down or model cold-loading | first Ollama call loads the model (slow); retry via reclassify/reembed |

Logs: backend logs to stdout (capture via `nohup` or launchd), one line per
classification/insert/split/embed outcome. There is no log file by default.

## 6. Environment variable reference

Go backend (`go-backend/.env`):

| Variable | Default | Empty means |
|---|---|---|
| `MDHUB_PG` | `postgres://mdhub:mdhub@localhost:5432/mdhub?sslmode=disable` | — (required) |
| `MDHUB_LISTEN` | `:10002` | — |
| `MDHUB_LLM_BASE_URL` | `https://api.openai.com/v1` | — |
| `MDHUB_LLM_API_KEY` | _(empty)_ | categorization disabled |
| `MDHUB_LLM_MODEL` | `gpt-4o-mini` | — |
| `MDHUB_EMBED_URL` | _(empty)_ | semantic search disabled |
| `MDHUB_EMBED_MODEL` | `qwen3-embedding:0.6b` | — |

Frontend (`.env.local`):

| Variable | Default |
|---|---|
| `MDHUB_API_URL` | `http://localhost:10002` |
| `MDHUB_PUBLIC_BASE_URL` | `http://localhost:10001/mdhub` |
| `MDHUB_EDIT_TOKEN` | _(unset = browser editing and uploads disabled)_ |
| `NEXT_PUBLIC_SEARCH_API` | `http://localhost:10002` |
| `NEXT_PUBLIC_HEARTH_URL` | _(unset = link hidden)_ |

## 7. Invariants worth knowing before changing code

- Slugs are stable public identifiers (`/view/<slug>/`); category changes never affect URLs.
- `category_path = ""` means "sits at tree root"; pinned notes (`category_manual = true`) are never moved by splits.
- Every tree node aims for ≤ 10 direct children; split decisions are local to one node, never global.
- Schema changes must stay idempotent (`IF NOT EXISTS` / `ALTER ... IF NOT EXISTS`) so 4.2 can always re-run `schema.sql`.
- Search and categorization never call external services on the read path; LLM/embedding work is write-time async only.
