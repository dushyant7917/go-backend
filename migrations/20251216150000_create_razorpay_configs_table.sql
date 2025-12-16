-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS razorpay_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_name VARCHAR(100) NOT NULL,
    environment VARCHAR(20) NOT NULL DEFAULT 'test',
    razorpay_key_id VARCHAR(255) NOT NULL,
    razorpay_key_secret VARCHAR(255) NOT NULL,
    razorpay_webhook_secret VARCHAR(255) NOT NULL,
    is_active BOOLEAN DEFAULT true,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP,
    UNIQUE(app_name, environment)
);

-- Create composite index on app_name and environment for fast lookups
CREATE INDEX idx_razorpay_configs_app_env ON razorpay_configs(app_name, environment) WHERE deleted_at IS NULL;

-- Create index on is_active for filtering active configs
CREATE INDEX idx_razorpay_configs_is_active ON razorpay_configs(is_active) WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_razorpay_configs_is_active;
DROP INDEX IF EXISTS idx_razorpay_configs_app_env;
DROP TABLE IF EXISTS razorpay_configs;
-- +goose StatementEnd
