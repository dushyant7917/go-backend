-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS r2_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_name VARCHAR(100) NOT NULL,
    environment VARCHAR(20) NOT NULL DEFAULT 'test',
    account_id VARCHAR(255) NOT NULL,
    access_key_id VARCHAR(255) NOT NULL,
    secret_access_key VARCHAR(255) NOT NULL,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP,
    UNIQUE(app_name, environment)
);

-- Create composite index on app_name and environment for fast lookups
CREATE INDEX idx_r2_configs_app_env ON r2_configs(app_name, environment) WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_r2_configs_app_env;
DROP TABLE IF EXISTS r2_configs;
-- +goose StatementEnd
