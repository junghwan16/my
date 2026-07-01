-- +goose Up
-- Full-text index over memory text for Korean-aware recall. Rows are populated
-- by the application (morpheme tokens joined by spaces), not by SQL, so this
-- migration only creates the empty index; a reindex backfills existing memories.
-- unicode61 (not trigram) is used because the app already tokenizes into
-- morphemes; trigram cannot match Korean query terms shorter than 3 characters.
CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
	memory_id UNINDEXED,
	tokens,
	tokenize = 'unicode61'
);

-- +goose Down
DROP TABLE IF EXISTS memories_fts;
