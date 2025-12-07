-- +goose Up
-- +goose StatementBegin

-- Drop old OTP table
DROP TABLE IF EXISTS otp;

-- Create phone_otp table
CREATE TABLE IF NOT EXISTS phone_otp (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_name VARCHAR(255) NOT NULL,
    country_code VARCHAR(5) NOT NULL,
    phone VARCHAR(20) NOT NULL,
    value VARCHAR(6) NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT (CURRENT_TIMESTAMP + interval '10 minutes'),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(app_name, country_code, phone)
);

-- Create index for faster lookups
CREATE INDEX idx_phone_otp_lookup ON phone_otp(app_name, country_code, phone);

-- Create email_otp table
CREATE TABLE IF NOT EXISTS email_otp (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_name VARCHAR(255) NOT NULL,
    email VARCHAR(255) NOT NULL,
    value VARCHAR(6) NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT (CURRENT_TIMESTAMP + interval '10 minutes'),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(app_name, email)
);

-- Create index for faster lookups
CREATE INDEX idx_email_otp_lookup ON email_otp(app_name, email);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS phone_otp;
DROP TABLE IF EXISTS email_otp;

-- Recreate old OTP table
CREATE TABLE IF NOT EXISTS otp (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    value VARCHAR(6) NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT (CURRENT_TIMESTAMP + interval '10 minutes'),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- +goose StatementEnd
