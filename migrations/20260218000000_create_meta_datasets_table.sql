-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS meta_datasets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_name VARCHAR(100) NOT NULL,
    environment VARCHAR(20) NOT NULL DEFAULT 'local',
    dataset_id VARCHAR(100) NOT NULL,
    access_token VARCHAR(500) NOT NULL,
    is_active BOOLEAN DEFAULT true,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP,
    UNIQUE(app_name, environment)
);

-- Create composite index on app_name and environment for fast lookups
CREATE INDEX idx_meta_datasets_app_env ON meta_datasets(app_name, environment) WHERE deleted_at IS NULL;

-- Create index on is_active for filtering active configs
CREATE INDEX idx_meta_datasets_is_active ON meta_datasets(is_active) WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_meta_datasets_is_active;
DROP INDEX IF EXISTS idx_meta_datasets_app_env;
DROP TABLE IF EXISTS meta_datasets;
-- +goose StatementEnd
