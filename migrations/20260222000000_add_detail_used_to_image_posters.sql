-- +goose Up
-- +goose StatementBegin
ALTER TABLE image_posters ADD COLUMN detail_used VARCHAR(255) NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE image_posters DROP CONSTRAINT IF EXISTS unique_poster_combo;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE image_posters ADD CONSTRAINT unique_poster_combo UNIQUE (user_id, template_id, name_used, profile_picture_key_used, detail_used);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE image_posters DROP CONSTRAINT IF EXISTS unique_poster_combo;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE image_posters DROP COLUMN detail_used;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE image_posters ADD CONSTRAINT unique_poster_combo UNIQUE (user_id, template_id, name_used, profile_picture_key_used);
-- +goose StatementEnd
