-- +goose Up
-- +goose StatementBegin

-- These indexes are redundant because UNIQUE constraints already create implicit unique indexes
DROP INDEX IF EXISTS idx_recurring_payments_token_id;
DROP INDEX IF EXISTS idx_payment_attempts_razorpay_payment_id;
DROP INDEX IF EXISTS idx_payment_attempts_razorpay_order_id;

-- Add missing index for razorpay_customer_id lookups
CREATE INDEX IF NOT EXISTS idx_recurring_payments_razorpay_customer_id ON recurring_payments(razorpay_customer_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE INDEX idx_recurring_payments_token_id ON recurring_payments(token_id);
CREATE INDEX idx_payment_attempts_razorpay_payment_id ON payment_attempts(razorpay_payment_id);
CREATE INDEX idx_payment_attempts_razorpay_order_id ON payment_attempts(razorpay_order_id);
DROP INDEX IF EXISTS idx_recurring_payments_razorpay_customer_id;

-- +goose StatementEnd
