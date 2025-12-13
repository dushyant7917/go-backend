-- +goose Up
-- +goose StatementBegin
-- Drop the old unique constraint on (country_code, phone)
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_phone_unique;

-- Add new unique constraint on (app_name, country_code, phone)
ALTER TABLE users ADD CONSTRAINT users_phone_unique UNIQUE (app_name, country_code, phone);

-- Drop the old unique constraint on email
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_email_unique;

-- Add new unique constraint on (app_name, email)
ALTER TABLE users ADD CONSTRAINT users_email_unique UNIQUE (app_name, email);

-- Update the indexes to match the new constraints
DROP INDEX IF EXISTS idx_users_country_phone;
CREATE INDEX IF NOT EXISTS idx_users_app_country_phone ON users(app_name, country_code, phone);

DROP INDEX IF EXISTS idx_users_email;
CREATE INDEX IF NOT EXISTS idx_users_app_email ON users(app_name, email);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Revert to old unique constraint on (country_code, phone)
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_phone_unique;
ALTER TABLE users ADD CONSTRAINT users_phone_unique UNIQUE (country_code, phone);

-- Revert to old unique constraint on email
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_email_unique;
ALTER TABLE users ADD CONSTRAINT users_email_unique UNIQUE (email);

-- Revert the indexes
DROP INDEX IF EXISTS idx_users_app_country_phone;
CREATE INDEX IF NOT EXISTS idx_users_country_phone ON users(country_code, phone);

DROP INDEX IF EXISTS idx_users_app_email;
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
-- +goose StatementEnd
