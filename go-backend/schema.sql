-- MDHub search backend – PostgreSQL schema
-- Run: psql -d mdhub -f schema.sql

CREATE TABLE IF NOT EXISTS documents (
    slug        TEXT PRIMARY KEY,
    file_path   TEXT NOT NULL,
    title       TEXT NOT NULL DEFAULT '',
    content     TEXT NOT NULL DEFAULT '',
    content_tsv TSVECTOR,
    word_count  INTEGER NOT NULL DEFAULT 0,
    published   BOOLEAN NOT NULL DEFAULT false,
    source      TEXT NOT NULL DEFAULT 'user',
    file_mtime  TIMESTAMPTZ NOT NULL DEFAULT now()
);

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

-- Full-text search index (auto-updated via trigger)
CREATE INDEX IF NOT EXISTS documents_tsv_idx ON documents USING GIN (content_tsv);

CREATE OR REPLACE FUNCTION documents_tsv_trigger() RETURNS trigger AS $$
BEGIN
    NEW.content_tsv := to_tsvector('simple', NEW.content);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_documents_tsv ON documents;
CREATE TRIGGER trg_documents_tsv
    BEFORE INSERT OR UPDATE OF content ON documents
    FOR EACH ROW EXECUTE FUNCTION documents_tsv_trigger();
