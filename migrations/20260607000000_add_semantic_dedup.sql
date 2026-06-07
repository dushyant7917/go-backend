-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS vector;

-- Embedding of the canonical article's base-language headline + summary (gemini-embedding-001, 768-dim).
ALTER TABLE news ADD COLUMN IF NOT EXISTS embedding vector(768);
CREATE INDEX IF NOT EXISTS idx_news_embedding ON news USING hnsw (embedding vector_cosine_ops);

-- Records articles dropped as semantic duplicates so later cron runs short-circuit them before any
-- LLM/embedding call. news_id points to the canonical (kept) article; cascade-deletes with it via
-- the existing cleanup_old_news job (1 canonical : N duplicates).
CREATE TABLE IF NOT EXISTS similar_news (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    news_id UUID NOT NULL REFERENCES news(id) ON DELETE CASCADE,
    link VARCHAR(2048) NOT NULL UNIQUE,
    content_hash VARCHAR(64),
    category VARCHAR(100),
    similarity_score REAL,
    source_host VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_similar_news_content_hash ON similar_news (content_hash) WHERE content_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_similar_news_news_id ON similar_news (news_id);

-- Records articles the LLM filter rejected (wrong category/state, astrology, recipe, ad) so later
-- cron runs short-circuit them before any translation call. No FK to news (a rejected item is not a
-- duplicate of any stored article); rows are age-pruned by the cleanup_old_news cron.
CREATE TABLE IF NOT EXISTS wrong_category_news (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    link VARCHAR(2048) NOT NULL UNIQUE,
    content_hash VARCHAR(64),
    category VARCHAR(100),
    skip_reason VARCHAR(500),
    source_host VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_wrong_category_news_content_hash ON wrong_category_news (content_hash) WHERE content_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_wrong_category_news_created_at ON wrong_category_news (created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS wrong_category_news;
DROP TABLE IF EXISTS similar_news;
DROP INDEX IF EXISTS idx_news_embedding;
ALTER TABLE news DROP COLUMN IF EXISTS embedding;
-- +goose StatementEnd
