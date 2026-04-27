-- +goose Up
CREATE TABLE meta_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    app_name VARCHAR(100) NOT NULL,
    name VARCHAR(100) NOT NULL,
    triggered BOOLEAN NOT NULL DEFAULT false,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_meta_events_pending ON meta_events(user_id, app_name, created_at) WHERE triggered = false;

-- +goose Down
DROP TABLE IF EXISTS meta_events;
