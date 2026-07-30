-- MDHub backend – PostgreSQL schema v2
-- Run: psql -d mdhub -f schema.sql
--
-- Postgres is the single source of truth: documents (including raw
-- markdown), images and comment threads all live here. Local vault files
-- are read once by `mdhub-go -import <vault-dir>` and then retired.
--
-- Note: /api/search matches in-process (Go memory), not via tsvector —
-- PG's text search parser treats CJK as whitespace on macOS.

CREATE TABLE IF NOT EXISTS documents (
    slug        TEXT PRIMARY KEY,
    file_path   TEXT NOT NULL,           -- original vault path (import provenance)
    title       TEXT NOT NULL DEFAULT '',
    content     TEXT NOT NULL DEFAULT '', -- plain text, for search/snippets
    raw_content TEXT NOT NULL DEFAULT '', -- original markdown incl. frontmatter
    excerpt     TEXT NOT NULL DEFAULT '', -- first 200 runes of plain text
    word_count  INTEGER NOT NULL DEFAULT 0,
    published   BOOLEAN NOT NULL DEFAULT false,
    source      TEXT NOT NULL DEFAULT 'user',
    category_path TEXT NOT NULL DEFAULT '', -- slash-separated path, e.g. "菜谱/家常"
    category_manual BOOLEAN NOT NULL DEFAULT false, -- frontmatter-pinned category, exempt from the tree algorithm
    file_mtime  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- idempotent migration for existing databases
ALTER TABLE documents ADD COLUMN IF NOT EXISTS category_path TEXT NOT NULL DEFAULT '';
ALTER TABLE documents ADD COLUMN IF NOT EXISTS category_manual BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS tags (
    name TEXT PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS document_tags (
    slug TEXT NOT NULL REFERENCES documents(slug) ON DELETE CASCADE,
    tag  TEXT NOT NULL REFERENCES tags(name) ON DELETE CASCADE,
    PRIMARY KEY (slug, tag)
);

CREATE TABLE IF NOT EXISTS backlinks (
    source_slug TEXT NOT NULL REFERENCES documents(slug) ON DELETE CASCADE,
    target_slug TEXT NOT NULL REFERENCES documents(slug) ON DELETE CASCADE,
    PRIMARY KEY (source_slug, target_slug)
);

CREATE TABLE IF NOT EXISTS images (
    path       TEXT PRIMARY KEY, -- vault-relative path, slash-separated
    data       BYTEA NOT NULL,
    mime       TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS comment_threads (
    id         TEXT PRIMARY KEY,
    slug       TEXT NOT NULL REFERENCES documents(slug) ON DELETE CASCADE,
    quote      TEXT NOT NULL DEFAULT '',
    prefix     TEXT NOT NULL DEFAULT '',
    suffix     TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS comment_entries (
    id         BIGSERIAL PRIMARY KEY,
    thread_id  TEXT NOT NULL REFERENCES comment_threads(id) ON DELETE CASCADE,
    author     TEXT NOT NULL,
    text       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- local embedding vectors for semantic search (Ollama, optional feature)
CREATE TABLE IF NOT EXISTS embeddings (
    slug      TEXT PRIMARY KEY REFERENCES documents(slug) ON DELETE CASCADE,
    embedding BYTEA NOT NULL,             -- float32 little-endian
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
