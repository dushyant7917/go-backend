-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS image_posters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    template_id UUID NOT NULL,
    name_used VARCHAR(255) NOT NULL,
    profile_picture_key_used VARCHAR(512) NOT NULL,
    file_key VARCHAR(512) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT fk_template FOREIGN KEY (template_id) REFERENCES image_templates(id) ON DELETE CASCADE,
    CONSTRAINT unique_poster_combo UNIQUE (user_id, template_id, name_used, profile_picture_key_used)
);

CREATE INDEX idx_image_posters_user_id ON image_posters(user_id);
CREATE INDEX idx_image_posters_template_id ON image_posters(template_id);
CREATE INDEX idx_image_posters_created_at ON image_posters(created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS image_posters;
-- +goose StatementEnd
