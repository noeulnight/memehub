-- +goose Up
CREATE TABLE collections (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100 AND name = btrim(name)),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, name)
);
CREATE INDEX collections_user_idx ON collections (user_id, created_at DESC, id DESC);
CREATE TABLE collection_memes (
    collection_id uuid NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    meme_id uuid NOT NULL REFERENCES memes(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (collection_id, meme_id)
);
CREATE INDEX collection_memes_meme_idx ON collection_memes (meme_id);

-- +goose Down
DROP TABLE collection_memes, collections;
