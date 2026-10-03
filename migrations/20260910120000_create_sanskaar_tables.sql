-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS devotional_music (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title VARCHAR(255) NOT NULL,
    deity VARCHAR(255) NOT NULL,
    category VARCHAR(255) NOT NULL CHECK (category IN ('mantra', 'bhajan', 'aarti', 'chalisa')),
    audio_file_key VARCHAR(512) NOT NULL UNIQUE,
    thumbnail_file_key VARCHAR(512) NOT NULL,
    duration DOUBLE PRECISION NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_devotional_music_deity ON devotional_music(deity);
CREATE INDEX idx_devotional_music_category ON devotional_music(category);
CREATE INDEX idx_devotional_music_created_at ON devotional_music(created_at DESC);

CREATE TABLE IF NOT EXISTS tones (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title VARCHAR(255) NOT NULL,
    deity VARCHAR(255) NOT NULL,
    audio_file_key VARCHAR(512) NOT NULL UNIQUE,
    thumbnail_file_key VARCHAR(512) NOT NULL,
    duration DOUBLE PRECISION NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_tones_deity ON tones(deity);
CREATE INDEX idx_tones_created_at ON tones(created_at DESC);

CREATE TABLE IF NOT EXISTS wallpapers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deity VARCHAR(255) NOT NULL,
    media_file_key VARCHAR(512) NOT NULL UNIQUE,
    type VARCHAR(20) NOT NULL CHECK (type IN ('image', 'video')),
    thumbnail_file_key VARCHAR(512),
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CHECK ((type = 'video' AND thumbnail_file_key IS NOT NULL) OR (type = 'image' AND thumbnail_file_key IS NULL))
);

CREATE INDEX idx_wallpapers_deity ON wallpapers(deity);
CREATE INDEX idx_wallpapers_created_at ON wallpapers(created_at DESC);

CREATE TABLE IF NOT EXISTS statuses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deity VARCHAR(255) NOT NULL,
    media_file_key VARCHAR(512) NOT NULL UNIQUE,
    type VARCHAR(20) NOT NULL CHECK (type IN ('image', 'video')),
    category VARCHAR(255) NOT NULL,
    aspect_ratio VARCHAR(10) NOT NULL CHECK (aspect_ratio IN ('4:5', '9:16')),
    thumbnail_file_key VARCHAR(512),
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CHECK ((type = 'video' AND thumbnail_file_key IS NOT NULL) OR (type = 'image' AND thumbnail_file_key IS NULL))
);

CREATE INDEX idx_statuses_deity ON statuses(deity);
CREATE INDEX idx_statuses_category ON statuses(category);
CREATE INDEX idx_statuses_created_at ON statuses(created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS statuses;
DROP TABLE IF EXISTS wallpapers;
DROP TABLE IF EXISTS tones;
DROP TABLE IF EXISTS devotional_music;
-- +goose StatementEnd
