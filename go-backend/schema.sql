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
ALTER TABLE documents ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'note'; -- 'note' | 'fleeting'

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

-- spark collisions: high-similarity document pairs found by the collision
-- engine (embedIndex cosine, see collide.go). slug_a < slug_b invariant.
CREATE TABLE IF NOT EXISTS collisions (
    id BIGSERIAL PRIMARY KEY,
    slug_a TEXT NOT NULL REFERENCES documents(slug) ON DELETE CASCADE,
    slug_b TEXT NOT NULL REFERENCES documents(slug) ON DELETE CASCADE,
    score DOUBLE PRECISION NOT NULL,
    explanation TEXT NOT NULL DEFAULT '',
    question TEXT NOT NULL DEFAULT '',
    verdict TEXT NOT NULL DEFAULT 'new',   -- new | confirmed | dismissed
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (slug_a, slug_b)
);

-- bounty answers: a collision's open question is claimed by writing a note
-- that answers it. No FK — the answer may be any document.
ALTER TABLE collisions ADD COLUMN IF NOT EXISTS answered_by TEXT NOT NULL DEFAULT '';
ALTER TABLE collisions ADD COLUMN IF NOT EXISTS answered_at TIMESTAMPTZ;

-- RSS/Atom subscriptions for the built-in poller (feed.go). There is no
-- items table: imported entries are documents under deterministic
-- _sparks/rss/<hash>/<hash> slugs, which is also the dedup mechanism.
CREATE TABLE IF NOT EXISTS feeds (
    id BIGSERIAL PRIMARY KEY,
    url TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    etag TEXT NOT NULL DEFAULT '',
    last_modified TEXT NOT NULL DEFAULT '',
    last_fetched_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- idempotent migration for existing databases: subscriber-written note about
-- what to watch for in this feed; appended to imported sparks so their
-- embeddings carry the subscriber's angle.
ALTER TABLE feeds ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';

-- Durable paper-translation work. Long translations are claimed by a
-- separately run worker and resume from persisted chunks after interruption.
CREATE TABLE IF NOT EXISTS translation_artifacts (
    hash       TEXT PRIMARY KEY,
    mime       TEXT NOT NULL,
    data       BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS translation_source_captures (
    id                TEXT PRIMARY KEY,
    source_input      TEXT NOT NULL,
    source_kind       TEXT NOT NULL,
    canonical_url     TEXT NOT NULL,
    artifact_url      TEXT NOT NULL,
    source_identifier TEXT NOT NULL DEFAULT '',
    source_version    TEXT NOT NULL DEFAULT '',
    source_title      TEXT NOT NULL DEFAULT '',
    series_key        TEXT NOT NULL,
    revision_key      TEXT NOT NULL,
    content_key       TEXT NOT NULL DEFAULT '',
    artifact_hash     TEXT REFERENCES translation_artifacts(hash),
    status            TEXT NOT NULL CHECK (status IN ('captured', 'needs_input')),
    size_bytes        BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at        TIMESTAMPTZ NOT NULL DEFAULT now() + interval '24 hours'
);

ALTER TABLE translation_source_captures
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '24 hours';

CREATE TABLE IF NOT EXISTS translation_jobs (
    id               TEXT PRIMARY KEY,
    source_input     TEXT NOT NULL,
    source_kind      TEXT NOT NULL,
    canonical_url    TEXT NOT NULL,
    artifact_url     TEXT NOT NULL,
    source_identifier TEXT NOT NULL DEFAULT '',
    source_version   TEXT NOT NULL DEFAULT '',
    source_title     TEXT NOT NULL DEFAULT '',
    source_hash      TEXT NOT NULL DEFAULT '',
    source_artifact_hash TEXT REFERENCES translation_artifacts(hash),
    target_language  TEXT NOT NULL DEFAULT 'zh-CN',
    profile          TEXT NOT NULL DEFAULT 'paper-translate-v1',
    state            TEXT NOT NULL DEFAULT 'queued',
    stage            TEXT NOT NULL DEFAULT 'queued',
    progress_current INTEGER NOT NULL DEFAULT 0,
    progress_total   INTEGER NOT NULL DEFAULT 0,
    lease_owner      TEXT NOT NULL DEFAULT '',
    lease_until      TIMESTAMPTZ,
    provider         TEXT NOT NULL DEFAULT '',
    model            TEXT NOT NULL DEFAULT '',
    output_slug      TEXT NOT NULL DEFAULT '',
    validation_report JSONB,
    error_summary    TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE translation_jobs ADD COLUMN IF NOT EXISTS source_manifest JSONB;
ALTER TABLE translation_jobs ADD COLUMN IF NOT EXISTS source_capture_id TEXT REFERENCES translation_source_captures(id);
ALTER TABLE translation_jobs ADD COLUMN IF NOT EXISTS source_series_key TEXT NOT NULL DEFAULT '';
ALTER TABLE translation_jobs ADD COLUMN IF NOT EXISTS source_revision_key TEXT NOT NULL DEFAULT '';
ALTER TABLE translation_jobs ADD COLUMN IF NOT EXISTS source_content_key TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS translation_jobs_active_revision_idx
    ON translation_jobs (source_revision_key, target_language, profile)
    WHERE source_revision_key <> '' AND state <> 'cancelled';

CREATE UNIQUE INDEX IF NOT EXISTS translation_jobs_active_content_idx
    ON translation_jobs (source_content_key, target_language, profile)
    WHERE source_content_key <> '' AND state <> 'cancelled';

CREATE INDEX IF NOT EXISTS translation_source_captures_expiry_idx
    ON translation_source_captures (expires_at);

CREATE INDEX IF NOT EXISTS translation_jobs_claim_idx
    ON translation_jobs (state, lease_until, created_at);

CREATE TABLE IF NOT EXISTS translation_chunks (
    job_id          TEXT NOT NULL REFERENCES translation_jobs(id) ON DELETE CASCADE,
    ordinal         INTEGER NOT NULL,
    source_text     TEXT NOT NULL,
    source_hash     TEXT NOT NULL,
    translated_text TEXT NOT NULL DEFAULT '',
    state           TEXT NOT NULL DEFAULT 'pending',
    attempts        INTEGER NOT NULL DEFAULT 0,
    provider        TEXT NOT NULL DEFAULT '',
    model           TEXT NOT NULL DEFAULT '',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (job_id, ordinal)
);

ALTER TABLE translation_chunks ADD COLUMN IF NOT EXISTS provider TEXT NOT NULL DEFAULT '';
ALTER TABLE translation_chunks ADD COLUMN IF NOT EXISTS model TEXT NOT NULL DEFAULT '';

UPDATE translation_chunks AS chunk
SET provider=job.provider, model=job.model
FROM translation_jobs AS job
WHERE chunk.job_id=job.id AND chunk.state='complete'
  AND chunk.provider='' AND job.provider<>'';
