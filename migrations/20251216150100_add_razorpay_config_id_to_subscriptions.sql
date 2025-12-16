-- +goose Up
-- +goose StatementBegin
-- Add razorpay_config_id column to subscriptions table
ALTER TABLE subscriptions ADD COLUMN razorpay_config_id UUID REFERENCES razorpay_configs(id);

-- Create index on razorpay_config_id for faster lookups
CREATE INDEX idx_subscriptions_razorpay_config_id ON subscriptions(razorpay_config_id);

-- Backfill existing subscriptions with a default razorpay_config_id (if needed)
-- This step requires manual intervention if there are existing subscriptions
-- You should create a default razorpay config first, then update this migration with its UUID
-- Example:
-- UPDATE subscriptions SET razorpay_config_id = 'your-default-config-uuid' WHERE razorpay_config_id IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_subscriptions_razorpay_config_id;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS razorpay_config_id;
-- +goose StatementEnd
