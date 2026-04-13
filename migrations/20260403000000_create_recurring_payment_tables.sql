-- +goose Up
-- +goose StatementBegin

-- RecurringPayments table (UPI Autopay mandate/authorization)
CREATE TABLE IF NOT EXISTS recurring_payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id UUID NOT NULL,
    app_name VARCHAR(100) NOT NULL,
    razorpay_customer_id VARCHAR(100),
    token_id VARCHAR(100) UNIQUE,
    status VARCHAR(50) NOT NULL DEFAULT 'created',
    max_amount BIGINT NOT NULL,
    frequency VARCHAR(50) NOT NULL,
    start_at TIMESTAMP WITH TIME ZONE,
    end_at TIMESTAMP WITH TIME ZONE,
    last_charged_at TIMESTAMP WITH TIME ZONE,
    next_charge_at TIMESTAMP WITH TIME ZONE,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    CONSTRAINT recurring_payments_status_check CHECK (status IN ('created', 'active', 'paused', 'cancelled', 'expired')),
    CONSTRAINT recurring_payments_frequency_check CHECK (frequency IN ('daily', 'weekly', 'fortnightly', 'bimonthly', 'monthly', 'quarterly', 'half_yearly', 'yearly', 'as_presented'))
);

CREATE INDEX IF NOT EXISTS idx_recurring_payments_user_id ON recurring_payments(user_id);
CREATE INDEX IF NOT EXISTS idx_recurring_payments_app_name ON recurring_payments(app_name);
CREATE INDEX IF NOT EXISTS idx_recurring_payments_token_id ON recurring_payments(token_id);
CREATE INDEX IF NOT EXISTS idx_recurring_payments_status ON recurring_payments(status);
CREATE INDEX IF NOT EXISTS idx_recurring_payments_next_charge_at ON recurring_payments(next_charge_at);
CREATE INDEX IF NOT EXISTS idx_recurring_payments_start_at ON recurring_payments(start_at);
CREATE INDEX IF NOT EXISTS idx_recurring_payments_last_charged_at ON recurring_payments(last_charged_at);
CREATE INDEX IF NOT EXISTS idx_recurring_payments_created_at ON recurring_payments(created_at);

-- BillingCycles table
CREATE TABLE IF NOT EXISTS billing_cycles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    recurring_payment_id UUID NOT NULL REFERENCES recurring_payments(id) ON DELETE CASCADE,
    cycle_number INTEGER NOT NULL,
    start_at TIMESTAMP WITH TIME ZONE NOT NULL,
    end_at TIMESTAMP WITH TIME ZONE,
    amount BIGINT NOT NULL,
    last_attempt_at TIMESTAMP WITH TIME ZONE,
    next_attempt_at TIMESTAMP WITH TIME ZONE,
    charge_attempts INTEGER DEFAULT 0,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT billing_cycles_status_check CHECK (status IN ('pending', 'paid', 'failed', 'skipped', 'cancelled')),
    CONSTRAINT billing_cycles_unique_recurring_payment_start UNIQUE (recurring_payment_id, start_at)
);

CREATE INDEX IF NOT EXISTS idx_billing_cycles_recurring_payment_id ON billing_cycles(recurring_payment_id);
CREATE INDEX IF NOT EXISTS idx_billing_cycles_status ON billing_cycles(status);
CREATE INDEX IF NOT EXISTS idx_billing_cycles_start_at ON billing_cycles(start_at);
CREATE INDEX IF NOT EXISTS idx_billing_cycles_next_attempt_at ON billing_cycles(next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_billing_cycles_created_at ON billing_cycles(created_at);

-- PaymentAttempts table
CREATE TABLE IF NOT EXISTS payment_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    billing_cycle_id UUID NOT NULL REFERENCES billing_cycles(id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL,
    razorpay_payment_id VARCHAR(100) UNIQUE,
    razorpay_order_id VARCHAR(100) UNIQUE,
    status VARCHAR(50) NOT NULL DEFAULT 'created',
    error_code VARCHAR(100),
    error_description TEXT,
    amount BIGINT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT payment_attempts_status_check CHECK (status IN ('created', 'authorized', 'pending', 'captured', 'failed')),
    CONSTRAINT payment_attempts_unique_billing_cycle_attempt UNIQUE (billing_cycle_id, attempt_number)
);

CREATE INDEX IF NOT EXISTS idx_payment_attempts_billing_cycle_id ON payment_attempts(billing_cycle_id);
CREATE INDEX IF NOT EXISTS idx_payment_attempts_razorpay_payment_id ON payment_attempts(razorpay_payment_id);
CREATE INDEX IF NOT EXISTS idx_payment_attempts_razorpay_order_id ON payment_attempts(razorpay_order_id);
CREATE INDEX IF NOT EXISTS idx_payment_attempts_status ON payment_attempts(status);
CREATE INDEX IF NOT EXISTS idx_payment_attempts_created_at ON payment_attempts(created_at);
CREATE INDEX IF NOT EXISTS idx_payment_attempts_error_code ON payment_attempts(error_code);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS payment_attempts;
DROP TABLE IF EXISTS billing_cycles;
DROP TABLE IF EXISTS recurring_payments;
-- +goose StatementEnd
