-- +goose Up
SELECT pg_advisory_xact_lock(hashtext('memehub:vector-extension'));
CREATE EXTENSION IF NOT EXISTS vector WITH SCHEMA public;

CREATE TABLE meme_embeddings (
    meme_id uuid PRIMARY KEY REFERENCES memes(id) ON DELETE CASCADE,
    model text NOT NULL CHECK (model = 'text-embedding-3-large'),
    input_version text NOT NULL CHECK (input_version = 'v1'),
    input_hash text NOT NULL,
    content_revision bigint NOT NULL,
    embedding public.vector(3072) NOT NULL,
    input_tokens bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE jobs DROP CONSTRAINT jobs_type_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_type_check CHECK (type IN ('prepare_image','analyze_content','delete_objects','embed_content'));
CREATE INDEX jobs_embedding_revision_idx ON jobs (meme_id,input_revision) WHERE type='embed_content';

-- IDs freeze ranking across pages; access and visibility are checked again when serving a page.
CREATE TABLE meme_searches (
    id uuid PRIMARY KEY,
    scope_hash text NOT NULL,
    meme_ids uuid[] NOT NULL CHECK (cardinality(meme_ids) <= 500),
    expires_at timestamptz NOT NULL DEFAULT now() + interval '10 minutes'
);
CREATE INDEX meme_searches_expiry_idx ON meme_searches (expires_at);

-- +goose Down
DROP TABLE meme_searches;
DELETE FROM jobs WHERE type='embed_content';
DROP INDEX jobs_embedding_revision_idx;
ALTER TABLE jobs DROP CONSTRAINT jobs_type_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_type_check CHECK (type IN ('prepare_image','analyze_content','delete_objects'));
DROP TABLE meme_embeddings;
-- The extension may be shared by other applications; leave it installed.
