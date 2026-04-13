-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS posthog_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_name VARCHAR(100) NOT NULL,
    environment VARCHAR(20) NOT NULL DEFAULT 'test',
    api_key VARCHAR(255) NOT NULL,
    host VARCHAR(255) NOT NULL,
    is_active BOOLEAN DEFAULT true,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    CONSTRAINT posthog_configs_app_env_unique UNIQUE (app_name, environment),
    CONSTRAINT posthog_configs_environment_check CHECK (environment IN ('test', 'live', 'local'))
);

CREATE INDEX IF NOT EXISTS idx_posthog_configs_app_name ON posthog_configs(app_name);
CREATE INDEX IF NOT EXISTS idx_posthog_configs_environment ON posthog_configs(environment);
CREATE INDEX IF NOT EXISTS idx_posthog_configs_is_active ON posthog_configs(is_active);
CREATE INDEX IF NOT EXISTS idx_posthog_configs_deleted_at ON posthog_configs(deleted_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS posthog_configs;
-- +goose StatementEnd
