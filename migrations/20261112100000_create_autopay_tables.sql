-- +goose Up
-- +goose StatementBegin

-- Credentials for ANY gateway, per app + environment. 'gateway' has no CHECK, so adding a gateway needs no migration.
-- encrypted_credentials: gateway-specific JSON, secret values AES-encrypted individually (pkg/secure/crypto.go):
--   razorpay {key_id, key_secret} | cashfree {client_id, client_secret} | phonepe {client_id, client_secret, client_version, merchant_id}
-- environment 'prod' = production API, anything else = sandbox. GO_ENV selects the config when a subscription is created.
CREATE TABLE payment_gateway_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    gateway VARCHAR(30) NOT NULL,
    app_name VARCHAR(100) NOT NULL,
    environment VARCHAR(20) NOT NULL,
    encrypted_credentials JSONB NOT NULL DEFAULT '{}'::jsonb,
    encrypted_webhook_secret VARCHAR(255) NOT NULL DEFAULT '',   -- the webhook route authenticates with it
    is_active BOOLEAN NOT NULL DEFAULT true,                     -- false = fully off (its webhook URL also returns 404)
    accepts_new_autopay BOOLEAN NOT NULL DEFAULT true,     -- false = kill switch: reject NEW subscriptions/one-time payments, keep serving existing ones and webhooks
    gateway_ratelimit_rpm INTEGER,                               -- per-account requests-per-minute override; NULL = the adapter's default (see its Capabilities)
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    CONSTRAINT ck_payment_gateway_configs_gateway_ratelimit_rpm_positive CHECK (gateway_ratelimit_rpm IS NULL OR gateway_ratelimit_rpm > 0)
);
CREATE UNIQUE INDEX uq_payment_gateway_configs_gateway_app_name_environment ON payment_gateway_configs(gateway, app_name, environment) WHERE deleted_at IS NULL;

-- Shared token bucket per config (= per merchant account) for the gateway's requests-per-minute limit; used by the server AND every cron.
-- tokens/refilled_at are only ever changed by the atomic UPDATE in §2c-bis (DB clock); rate and burst come from the caller (adapter limit - buffer).
CREATE TABLE payment_gateway_rate_limits (
    gateway_config_id UUID PRIMARY KEY,
    tokens DOUBLE PRECISION NOT NULL,                            -- tokens currently available (0..burst)
    refilled_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT fk_payment_gateway_rate_limits_gateway_config_id FOREIGN KEY (gateway_config_id) REFERENCES payment_gateway_configs(id) ON DELETE CASCADE,
    CONSTRAINT ck_payment_gateway_rate_limits_tokens_non_negative CHECK (tokens >= 0)
);

-- A customer subscription: a recurring mandate OR a one-time payment. Amounts are integers in the currency's minor unit (paise for INR).
CREATE TABLE autopay_mandates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),               -- the autopay_mandate_id returned to the client
    user_id UUID NOT NULL,
    app_name VARCHAR(100) NOT NULL,
    gateway_config_id UUID NOT NULL,                             -- the client's `gateway` request field resolves to this config (gateway + app + environment = ONE merchant account), stored ONCE at creation; the gateway name is read from the config (no separate gateway column)
    payment_type VARCHAR(30) NOT NULL,                      -- 'recurring' | 'one_time'
    merchant_autopay_id VARCHAR(100) NOT NULL,              -- issued by US (merchant) and sent to the gateway as our reference (Razorpay has none: row UUID text); one-time: our order id. The gateway's own id is gateway_mandate_id (rule: gateway_* = issued by the gateway, merchant_* = issued by us)
    gateway_mandate_id VARCHAR(100),                             -- recurring only: Razorpay token_id | Cashfree cf_subscription_id | PhonePe subscriptionId; NULL for one-time
    gateway_customer_id VARCHAR(100),                            -- Razorpay customer id
    status VARCHAR(30) NOT NULL DEFAULT 'awaiting_authorization',
    max_debit_amount BIGINT NOT NULL,                            -- recurring: cap per debit; one-time: the payment amount
    currency CHAR(3) NOT NULL DEFAULT 'INR',
    billing_frequency VARCHAR(30) NOT NULL,                      -- how often WE debit; the gateway-side NPCI pattern (ON_DEMAND / "as presented") is an adapter constant, not a value here
    start_at TIMESTAMP WITH TIME ZONE,
    end_at TIMESTAMP WITH TIME ZONE,
    last_successful_charge_at TIMESTAMP WITH TIME ZONE,
    next_charge_due_at TIMESTAMP WITH TIME ZONE,                 -- NULL for one-time so the billing crons skip them
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    CONSTRAINT fk_autopay_mandates_user_id FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,   -- hard-deleting a user removes their mandates, and (via the cascades below) cycles and attempts; the gateway-side mandate is NOT touched, so cancel it at the gateway first
    CONSTRAINT fk_autopay_mandates_gateway_config_id FOREIGN KEY (gateway_config_id) REFERENCES payment_gateway_configs(id),
    CONSTRAINT ck_autopay_mandates_payment_type_valid CHECK (payment_type IN ('recurring','one_time')),
    CONSTRAINT ck_autopay_mandates_status_valid CHECK (status IN ('awaiting_authorization','active','paused','cancelled','expired')),
    CONSTRAINT ck_autopay_mandates_currency_format_valid CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT ck_autopay_mandates_max_debit_amount_positive CHECK (max_debit_amount > 0),
    CONSTRAINT ck_autopay_mandates_billing_frequency_valid CHECK (billing_frequency IN ('daily','weekly','fortnightly','bimonthly','monthly','quarterly','half_yearly','yearly'))
);
CREATE UNIQUE INDEX uq_autopay_mandates_merchant_autopay_id ON autopay_mandates(merchant_autopay_id);   -- our own id: globally unique by construction, so no gateway scoping needed
CREATE UNIQUE INDEX uq_autopay_mandates_config_id_gateway_mandate_id ON autopay_mandates(gateway_config_id, gateway_mandate_id) WHERE gateway_mandate_id IS NOT NULL;   -- gateway-issued: unique per merchant account
CREATE INDEX idx_autopay_mandates_user_id_app_name ON autopay_mandates(user_id, app_name);
CREATE INDEX idx_autopay_mandates_next_charge_due_at_active ON autopay_mandates(next_charge_due_at) WHERE status = 'active' AND deleted_at IS NULL;

-- Billing cycles (cycle_number 0 = mandate authorization, or the single one-time payment; 1.. = real billing)
CREATE TABLE autopay_billing_cycles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    autopay_mandate_id UUID NOT NULL,
    cycle_number INTEGER NOT NULL,
    start_at TIMESTAMP WITH TIME ZONE NOT NULL,
    end_at TIMESTAMP WITH TIME ZONE,
    amount BIGINT NOT NULL,                                -- in the parent's currency (no currency column here: the parent autopay_mandates row owns it)
    last_charge_attempt_at TIMESTAMP WITH TIME ZONE,
    next_charge_attempt_at TIMESTAMP WITH TIME ZONE,
    counted_charge_attempts INTEGER NOT NULL DEFAULT 0,          -- only attempts that count toward the 9 (drives the reduced-amount ladder)
    status VARCHAR(30) NOT NULL DEFAULT 'awaiting_payment',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_autopay_billing_cycles_autopay_mandate_id FOREIGN KEY (autopay_mandate_id) REFERENCES autopay_mandates(id) ON DELETE CASCADE,
    CONSTRAINT ck_autopay_billing_cycles_status_valid CHECK (status IN ('awaiting_payment','paid','failed_all_attempts','skipped','cancelled')),
    CONSTRAINT ck_autopay_billing_cycles_amount_positive CHECK (amount > 0),
    CONSTRAINT uq_autopay_billing_cycles_autopay_mandate_id_start_at UNIQUE (autopay_mandate_id, start_at),
    CONSTRAINT uq_autopay_billing_cycles_autopay_mandate_id_cycle_number UNIQUE (autopay_mandate_id, cycle_number)
);
CREATE INDEX idx_autopay_billing_cycles_next_charge_attempt_at_open ON autopay_billing_cycles(next_charge_attempt_at) WHERE status = 'awaiting_payment';

-- One charge attempt = one pre-debit notification + one debit. merchant_payment_id is OUR id; gateway ids live in the gateway_* columns.
CREATE TABLE autopay_charge_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    billing_cycle_id UUID NOT NULL,
    gateway_config_id UUID NOT NULL,                             -- immutable copy of the mandate's config (merchant account): the ONE denormalized column, needed so the unique indexes on gateway-issued order/payment/link ids are enforced per merchant account and webhook lookups are scoped by the URL's config; cycles are never looked up by a gateway id, so they do not carry it. No FK on purpose: the value is copied from the mandate, whose own FK already guarantees the config exists and blocks its deletion
    attempt_number INTEGER NOT NULL,
    counts_toward_attempt_limit BOOLEAN NOT NULL DEFAULT true,   -- false for gateway_unavailable failures (do not use up one of the 9)
    merchant_payment_id VARCHAR(100) NOT NULL,
    gateway_order_id VARCHAR(100),                               -- Razorpay order | PhonePe order id
    gateway_payment_id VARCHAR(100),                             -- Razorpay payment | Cashfree cf_payment_id | PhonePe transaction id
    gateway_payment_link_id VARCHAR(100),                        -- one-time payment links (Razorpay / Cashfree)
    pre_debit_notification_id VARCHAR(120),
    pre_debit_notification_status VARCHAR(50),
    pre_debit_notification_send_attempts INTEGER NOT NULL DEFAULT 0,
    pre_debit_notification_sent_at TIMESTAMP WITH TIME ZONE,     -- when our successful send call returned (gate-clock fallback)
    pre_debit_notification_delivered_at TIMESTAMP WITH TIME ZONE,-- gateway's delivered time (gate clock): debit needs <= now - 26h
    debit_execution_id VARCHAR(120),
    debit_execution_status VARCHAR(50),
    debit_submit_attempts INTEGER NOT NULL DEFAULT 0,
    debit_temporary_rejection_count INTEGER NOT NULL DEFAULT 0,  -- RejectedTemporarily count (was metadata.execute_errors; limit 3 → now 2)
    debit_gateway_unavailable_retry_count INTEGER NOT NULL DEFAULT 0, -- gateway-unavailable releases (limit 2)
    status VARCHAR(30) NOT NULL DEFAULT 'awaiting_notification',
    failure_category VARCHAR(40),                                -- rejected_permanently | gateway_unavailable | notification_failed
    failure_code VARCHAR(255),
    failure_description TEXT,
    amount BIGINT NOT NULL,                               -- in the parent's currency (no currency column here)
    last_gateway_event_at TIMESTAMP WITH TIME ZONE,              -- event time of the newest webhook applied (ordering guard)
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_autopay_charge_attempts_billing_cycle_id FOREIGN KEY (billing_cycle_id) REFERENCES autopay_billing_cycles(id) ON DELETE CASCADE,
    CONSTRAINT uq_autopay_charge_attempts_merchant_payment_id UNIQUE (merchant_payment_id),
    CONSTRAINT ck_autopay_charge_attempts_status_valid CHECK (status IN ('awaiting_notification','notification_delivered','debit_submitted','debit_succeeded','debit_failed')),
    CONSTRAINT ck_autopay_charge_attempts_amount_positive CHECK (amount > 0)
);
-- a non-counted (gateway_unavailable) attempt reuses its number, so uniqueness covers counted attempts only
CREATE UNIQUE INDEX uq_autopay_charge_attempts_cycle_attempt_number_counted ON autopay_charge_attempts(billing_cycle_id, attempt_number) WHERE counts_toward_attempt_limit;
CREATE UNIQUE INDEX uq_autopay_charge_attempts_config_id_gateway_order_id ON autopay_charge_attempts(gateway_config_id, gateway_order_id) WHERE gateway_order_id IS NOT NULL;
CREATE UNIQUE INDEX uq_autopay_charge_attempts_config_id_gateway_payment_id ON autopay_charge_attempts(gateway_config_id, gateway_payment_id) WHERE gateway_payment_id IS NOT NULL;
CREATE UNIQUE INDEX uq_autopay_charge_attempts_config_id_gateway_payment_link_id ON autopay_charge_attempts(gateway_config_id, gateway_payment_link_id) WHERE gateway_payment_link_id IS NOT NULL;
CREATE INDEX idx_autopay_charge_attempts_billing_cycle_id ON autopay_charge_attempts(billing_cycle_id);
-- ONE partial index for all the cron work queues: the set of OPEN attempts is tiny compared with the full history, so each cron's query
-- (debit candidates, attempts awaiting notification, attempts awaiting a debit result) filters by status inside this small index and applies its time filter to a handful of rows.
CREATE INDEX idx_autopay_charge_attempts_open_attempts ON autopay_charge_attempts(status, created_at) WHERE status IN ('awaiting_notification','notification_delivered','debit_submitted');

-- Verified webhook receipts: dedupe, audit, replay. Written only after the signature verified.
CREATE TABLE autopay_webhook_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    gateway_config_id UUID NOT NULL,                             -- the config (merchant account) whose webhook URL received the event; the gateway name comes from it
    event_dedupe_key VARCHAR(200) NOT NULL,                      -- gateway event id when it has one, else a hash of identifying payload keys, else sha256(raw body)
    gateway_event_type VARCHAR(100),
    gateway_entity_id VARCHAR(100),                              -- order / payment / subscription / token id from the event
    gateway_event_at TIMESTAMP WITH TIME ZONE,                   -- event time from the payload (NULL if absent)
    received_headers JSONB NOT NULL DEFAULT '{}'::jsonb,
    raw_body TEXT NOT NULL,                                      -- exact bytes (re-verifiable, replayable)
    status VARCHAR(30) NOT NULL DEFAULT 'received',   -- received | processed | ignored_unknown_entity | refetch_required | failed
    processing_error TEXT,
    received_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    processed_at TIMESTAMP WITH TIME ZONE,
    CONSTRAINT fk_autopay_webhook_events_gateway_config_id FOREIGN KEY (gateway_config_id) REFERENCES payment_gateway_configs(id) ON DELETE CASCADE,   -- receipts are a replay/audit log of that account's webhooks, not payment records: they go with the config
    CONSTRAINT ck_autopay_webhook_events_status_valid CHECK (status IN ('received','processed','ignored_unknown_entity','refetch_required','failed')),
    CONSTRAINT uq_autopay_webhook_events_config_id_event_dedupe_key UNIQUE (gateway_config_id, event_dedupe_key)
);
CREATE INDEX idx_autopay_webhook_events_unprocessed ON autopay_webhook_events(received_at) WHERE status IN ('received','refetch_required','failed');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS autopay_webhook_events;
DROP TABLE IF EXISTS autopay_charge_attempts;
DROP TABLE IF EXISTS autopay_billing_cycles;
DROP TABLE IF EXISTS autopay_mandates;
DROP TABLE IF EXISTS payment_gateway_rate_limits;
DROP TABLE IF EXISTS payment_gateway_configs;
-- +goose StatementEnd
