-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS news (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    link VARCHAR(2048) NOT NULL UNIQUE,
    media_file_key VARCHAR(512),
    category VARCHAR(100) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'published',
    published_at TIMESTAMP WITH TIME ZONE,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT news_status_check CHECK (status IN ('published', 'approved', 'rejected'))
);

CREATE INDEX IF NOT EXISTS idx_news_category ON news(category);
CREATE INDEX IF NOT EXISTS idx_news_published_at ON news(published_at);
CREATE INDEX IF NOT EXISTS idx_news_status ON news(status);
CREATE INDEX IF NOT EXISTS idx_news_created_at ON news(created_at);

CREATE TABLE IF NOT EXISTS news_translations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    news_id UUID NOT NULL REFERENCES news(id) ON DELETE CASCADE,
    title VARCHAR(1000) NOT NULL,
    language_code VARCHAR(10) NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_news_translations_language_code ON news_translations(language_code);
CREATE INDEX IF NOT EXISTS idx_news_translations_news_id ON news_translations(news_id);
CREATE INDEX IF NOT EXISTS idx_news_translations_title ON news_translations(title);
CREATE INDEX IF NOT EXISTS idx_news_translations_created_at ON news_translations(created_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_news_translations_news_id_language_code ON news_translations(news_id, language_code);

CREATE TABLE IF NOT EXISTS news_posters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    news_id UUID NOT NULL REFERENCES news(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    profile_picture_key VARCHAR(512),
    user_name VARCHAR(255) NOT NULL,
    user_detail VARCHAR(500),
    user_state_id VARCHAR(100) NOT NULL,
    language_code VARCHAR(10) NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_news_posters_news_id ON news_posters(news_id);
CREATE INDEX IF NOT EXISTS idx_news_posters_user_id ON news_posters(user_id);
CREATE INDEX IF NOT EXISTS idx_news_posters_user_state_id ON news_posters(user_state_id);
CREATE INDEX IF NOT EXISTS idx_news_posters_language_code ON news_posters(language_code);
CREATE INDEX IF NOT EXISTS idx_news_posters_created_at ON news_posters(created_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS news_posters;
DROP TABLE IF EXISTS news_translations;
DROP TABLE IF EXISTS news;
-- +goose StatementEnd
