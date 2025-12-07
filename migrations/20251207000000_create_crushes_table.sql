-- +goose Up
-- +goose StatementBegin
CREATE TABLE crushes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    country_code VARCHAR(10),
    phone VARCHAR(20),
    instagram_id VARCHAR(255),
    snapchat_id VARCHAR(255),
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

-- Create index on user_id for efficient listing
CREATE INDEX idx_crushes_user_id ON crushes(user_id) WHERE deleted_at IS NULL;

-- Add constraint to ensure at least one contact method is provided
ALTER TABLE crushes ADD CONSTRAINT chk_crushes_contact_method 
    CHECK (
        (country_code IS NOT NULL AND phone IS NOT NULL) OR 
        (instagram_id IS NOT NULL AND instagram_id != '') OR 
        (snapchat_id IS NOT NULL AND snapchat_id != '')
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS crushes;
-- +goose StatementEnd
