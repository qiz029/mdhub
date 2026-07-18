# Markdown Hub

A self-hosted, local-first markdown publishing system. Reads an Obsidian vault from the filesystem, serves notes with `publish: true` in YAML frontmatter as a web feed — like a family Obsidian Publish, but local.

## Architecture

```
┌─────────────────┐     ┌──────────────────┐
│  Obsidian Vault │────▶│  Next.js (10001)  │  ← Markdown feed + viewer
│  (filesystem)   │     └────────┬─────────┘
└─────────────────┘              │ client-side fetch
                                 ▼
                        ┌──────────────────┐
                        │  Go API (10002)   │  ← Full-text search (bigram)
                        │  PostgreSQL       │
                        └──────────────────┘
```

- **Frontend**: Next.js 16 (App Router) — scans vault filesystem, renders markdown, serves local images
- **Search backend**: Go binary — Chinese bigram tokenizer, PostgreSQL full-text search, file watcher
- **No database required** for basic use; search needs PostgreSQL

## Quick Start

### 1. Frontend

```bash
cp .env.example .env.local
# Edit .env.local: set MDHUB_VAULT_PATH to your Obsidian vault
npm install
npm run dev        # → http://localhost:10001/mdhub
```

### 2. Search backend (optional)

```bash
cd go-backend
cp .env.example .env
# Edit .env: set MDHUB_VAULT and MDHUB_PG
psql -d mdhub -f schema.sql   # create tables
go build -o mdhub-go .
./mdhub-go                     # → http://localhost:10002
```

Then set `NEXT_PUBLIC_SEARCH_API=http://localhost:10002` in the frontend's `.env.local`.

## Publishing a note

Add to any `.md` file in your vault:

```yaml
---
title: My Note
publish: true
date: 2025-01-01
tags: [foo, bar]
---
```

The home page lists all published notes, newest first. Each gets a URL at `/view/<vault-relative-path>/`.

## Environment Variables

### Frontend (`.env.local`)

| Variable | Default | Description |
|---|---|---|
| `MDHUB_VAULT_PATH` | `~/Documents/Obsidian Vault` | Path to Obsidian vault |
| `MDHUB_PUBLIC_BASE_URL` | `http://localhost:10001/mdhub` | Public base URL |
| `NEXT_PUBLIC_SEARCH_API` | `http://localhost:10002` | Search backend URL (browser → Go) |
| `NEXT_PUBLIC_HEARTH_URL` | _(unset)_ | Optional "back to Hearth" link |

### Go Backend (`.env`)

| Variable | Default | Description |
|---|---|---|
| `MDHUB_VAULT` | `~/Documents/Obsidian Vault/_translations` | Path to markdown files |
| `MDHUB_PG` | `postgres://mdhub:***@localhost:5432/mdhub` | PostgreSQL DSN |
| `MDHUB_LISTEN` | `:10002` | Listen address |

## Features

- **Filesystem-first**: Vault is the source of truth. No upload, no database sync needed for reading.
- **Frontmatter-driven**: `publish: true` controls visibility; `tags` enable filtering.
- **Chinese search**: Bigram tokenizer + PostgreSQL tsvector — handles CJK text well.
- **Local images**: Vault-relative image paths served through `/api/image`.
- **Font presets**: 6 Chinese font options (system, serif, kai, hei, wenkai, fangsong).
- **⌘K search**: Fuzzy full-text search with inline snippets.

## Production deploy

```bash
npm run build
npm start            # Next.js on :10001
# In go-backend/:
go build -o mdhub-go && ./run.sh   # Go API on :10002
```

Launchd plist examples for macOS are available — see `scripts/start.sh` and `go-backend/run.sh` for the entry points.

## License

MIT
