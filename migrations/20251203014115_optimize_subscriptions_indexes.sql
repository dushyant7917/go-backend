-- +goose Up
-- +goose StatementBegin

-- Drop ALL unnecessary indexes that slow down INSERTs
-- Only keep indexes actually used by /verify and /webhook endpoints
DROP INDEX IF EXISTS idx_subscriptions_user_id;
DROP INDEX IF EXISTS idx_subscriptions_app_name;
DROP INDEX IF EXISTS idx_subscriptions_razorpay_customer_id;
DROP INDEX IF EXISTS idx_subscriptions_status;
DROP INDEX IF EXISTS idx_subscriptions_deleted_at;
DROP INDEX IF EXISTS idx_subscriptions_created_at;
DROP INDEX IF EXISTS idx_subscriptions_user_app;

-- ONLY keep the unique index on razorpay_subscription_id
-- This is the ONLY field queried in /verify and /webhook endpoints
-- The unique constraint already creates an index, so no additional index needed

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Restore original indexes if rolling back
CREATE INDEX IF NOT EXISTS idx_subscriptions_user_id ON subscriptions(user_id);
CREATE INDEX IF NOT EXISTS idx_subscriptions_app_name ON subscriptions(app_name);
CREATE INDEX IF NOT EXISTS idx_subscriptions_razorpay_customer_id ON subscriptions(razorpay_customer_id);
CREATE INDEX IF NOT EXISTS idx_subscriptions_status ON subscriptions(status);
CREATE INDEX IF NOT EXISTS idx_subscriptions_deleted_at ON subscriptions(deleted_at);
CREATE INDEX IF NOT EXISTS idx_subscriptions_created_at ON subscriptions(created_at);
CREATE INDEX IF NOT EXISTS idx_subscriptions_user_app ON subscriptions(user_id, app_name);

-- +goose StatementEnd
