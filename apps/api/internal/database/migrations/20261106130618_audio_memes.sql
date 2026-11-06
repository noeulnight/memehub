-- +goose Up
ALTER TABLE memes DROP CONSTRAINT memes_content_type_check;
ALTER TABLE memes DROP CONSTRAINT memes_check;
ALTER TABLE memes DROP CONSTRAINT memes_check1;
ALTER TABLE memes ADD CONSTRAINT memes_content_type_check CHECK (content_type IN ('image','text','audio'));
ALTER TABLE memes ADD CONSTRAINT memes_check CHECK (
 (content_type IN ('image','audio') AND original_filename IS NOT NULL AND original_object_key IS NOT NULL AND body_text IS NULL)
 OR (content_type='text' AND body_text IS NOT NULL AND body_text ~ '[^[:space:]]'
 AND original_filename IS NULL AND original_object_key IS NULL AND thumbnail_object_key IS NULL
 AND mime_type IS NULL AND size_bytes IS NULL AND width IS NULL AND height IS NULL AND sha256 IS NULL
 AND extracted_text='' AND status='ready')
);
ALTER TABLE memes ADD CONSTRAINT memes_check1 CHECK (
 content_type NOT IN ('image','audio') OR status<>'ready' OR
 (mime_type IS NOT NULL AND size_bytes IS NOT NULL AND sha256 IS NOT NULL
 AND (content_type='audio' OR (width IS NOT NULL AND height IS NOT NULL)))
);
ALTER TABLE memes ADD CONSTRAINT memes_audio_check CHECK (content_type<>'audio' OR
 (mime_type IN ('audio/mpeg','audio/wav','audio/ogg','audio/mp4') AND width IS NULL AND height IS NULL
 AND thumbnail_object_key IS NULL AND preview_object_key IS NULL AND analysis_status='not_requested'));
DROP INDEX memes_cleanup_idx;
CREATE INDEX memes_cleanup_idx ON memes(updated_at) WHERE content_type IN ('image','audio') AND objects_deleted_at IS NULL;

-- +goose Down
-- Refuse rollback while audio records exist rather than deleting user content.
ALTER TABLE memes DROP CONSTRAINT memes_audio_check;
ALTER TABLE memes DROP CONSTRAINT memes_content_type_check;
ALTER TABLE memes ADD CONSTRAINT memes_content_type_check CHECK (content_type IN ('image','text'));
ALTER TABLE memes DROP CONSTRAINT memes_check;
ALTER TABLE memes ADD CONSTRAINT memes_check CHECK (
 (content_type='image' AND original_filename IS NOT NULL AND original_object_key IS NOT NULL AND body_text IS NULL)
 OR (content_type='text' AND body_text IS NOT NULL AND body_text ~ '[^[:space:]]'
 AND original_filename IS NULL AND original_object_key IS NULL AND thumbnail_object_key IS NULL
 AND mime_type IS NULL AND size_bytes IS NULL AND width IS NULL AND height IS NULL AND sha256 IS NULL
 AND extracted_text='' AND status='ready')
);
ALTER TABLE memes DROP CONSTRAINT memes_check1;
ALTER TABLE memes ADD CONSTRAINT memes_check1 CHECK (content_type<>'image' OR status<>'ready' OR
 (mime_type IS NOT NULL AND size_bytes IS NOT NULL AND width IS NOT NULL AND height IS NOT NULL AND sha256 IS NOT NULL));
DROP INDEX memes_cleanup_idx;
CREATE INDEX memes_cleanup_idx ON memes(updated_at) WHERE content_type='image' AND objects_deleted_at IS NULL;
