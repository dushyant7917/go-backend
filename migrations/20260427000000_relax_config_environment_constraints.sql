-- +goose Up
-- +goose StatementBegin

-- Drop CHECK constraint on posthog_configs.environment (only table with one)
ALTER TABLE posthog_configs DROP CONSTRAINT IF EXISTS posthog_configs_environment_check;

-- Remove stale defaults — environment should always be explicitly provided
ALTER TABLE razorpay_configs ALTER COLUMN environment DROP DEFAULT;
ALTER TABLE meta_datasets ALTER COLUMN environment DROP DEFAULT;
ALTER TABLE r2_configs ALTER COLUMN environment DROP DEFAULT;
ALTER TABLE posthog_configs ALTER COLUMN environment DROP DEFAULT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Restore defaults
ALTER TABLE razorpay_configs ALTER COLUMN environment SET DEFAULT 'test';
ALTER TABLE meta_datasets ALTER COLUMN environment SET DEFAULT 'local';
ALTER TABLE r2_configs ALTER COLUMN environment SET DEFAULT 'test';
ALTER TABLE posthog_configs ALTER COLUMN environment SET DEFAULT 'test';

-- Restore CHECK constraint
ALTER TABLE posthog_configs ADD CONSTRAINT posthog_configs_environment_check CHECK (environment IN ('test', 'live', 'local'));

-- +goose StatementEnd
