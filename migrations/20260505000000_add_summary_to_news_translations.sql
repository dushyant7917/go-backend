-- +goose Up
-- +goose StatementBegin
ALTER TABLE news_translations ADD COLUMN IF NOT EXISTS summary VARCHAR(1000);
UPDATE news_translations SET summary = title;
ALTER TABLE news_translations ALTER COLUMN summary SET NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE news_translations DROP COLUMN IF EXISTS summary;
-- +goose StatementEnd
