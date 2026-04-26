-- +goose Up
-- +goose StatementBegin
ALTER TABLE meta_datasets ADD COLUMN IF NOT EXISTS app_id VARCHAR(100) DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE meta_datasets DROP COLUMN IF EXISTS app_id;
-- +goose StatementEnd
