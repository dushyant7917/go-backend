-- +goose Up
-- +goose StatementBegin
ALTER TABLE news ADD COLUMN IF NOT EXISTS sub_category VARCHAR(100);
ALTER TABLE news ADD COLUMN IF NOT EXISTS image_prompt TEXT;
CREATE INDEX IF NOT EXISTS idx_news_category_sub_category ON news(category, sub_category);
ALTER TABLE similar_news ADD COLUMN IF NOT EXISTS sub_category VARCHAR(100);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE similar_news DROP COLUMN IF EXISTS sub_category;
DROP INDEX IF EXISTS idx_news_category_sub_category;
ALTER TABLE news DROP COLUMN IF EXISTS image_prompt;
ALTER TABLE news DROP COLUMN IF EXISTS sub_category;
-- +goose StatementEnd
