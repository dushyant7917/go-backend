-- +goose Up
-- +goose StatementBegin
ALTER TABLE news ADD COLUMN IF NOT EXISTS content_hash VARCHAR(64);
CREATE UNIQUE INDEX idx_news_content_hash ON news (content_hash) WHERE content_hash IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_news_content_hash;
ALTER TABLE news DROP COLUMN IF EXISTS content_hash;
-- +goose StatementEnd
