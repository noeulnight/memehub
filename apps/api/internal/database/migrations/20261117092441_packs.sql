-- +goose Up
CREATE TABLE packs (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100 AND name = btrim(name)),
    source_url text,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX packs_name_idx ON packs (lower(name));
ALTER TABLE memes ADD COLUMN pack_id uuid REFERENCES packs(id) ON DELETE SET NULL;
CREATE INDEX memes_pack_idx ON memes (pack_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL AND status = 'ready';

-- +goose Down
ALTER TABLE memes DROP COLUMN pack_id;
DROP TABLE packs;
