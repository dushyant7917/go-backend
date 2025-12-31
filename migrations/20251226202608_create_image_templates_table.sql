-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS image_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_key VARCHAR(512) NOT NULL UNIQUE,
    category VARCHAR(255) NOT NULL,
    sub_category VARCHAR(255) NOT NULL,
    config JSONB,
    metadata JSONB,
    author_id UUID,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_author FOREIGN KEY (author_id) REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_image_templates_category ON image_templates(category);
CREATE INDEX idx_image_templates_sub_category ON image_templates(sub_category);
CREATE INDEX idx_image_templates_author_id ON image_templates(author_id);
CREATE INDEX idx_image_templates_created_at ON image_templates(created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS image_templates;
-- +goose StatementEnd
