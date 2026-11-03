-- +goose Up
-- Keep original metadata unchanged; normalize fields and queries identically.
-- Preserve literal SQL wildcard characters so 100_ cannot match 100%.
-- +goose StatementBegin
CREATE FUNCTION search_normalize(value text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE
RETURN regexp_replace(lower(normalize(COALESCE(value,''), NFKC)), '[^[:alnum:]%_]', '', 'g');
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION search_normalize(text);
