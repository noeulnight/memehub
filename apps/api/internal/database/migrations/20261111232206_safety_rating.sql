-- +goose Up
ALTER TABLE memes ADD COLUMN safety_rating text NOT NULL DEFAULT 'unknown'
 CHECK (safety_rating IN ('sfw','nsfw','unknown'));

-- +goose Down
ALTER TABLE memes DROP COLUMN safety_rating;
