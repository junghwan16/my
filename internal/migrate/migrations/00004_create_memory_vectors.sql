-- +goose Up
-- Dense embeddings for optional semantic recall. Runs after 00002, so
-- memories(id) exists for the foreign key. One vector per memory, stored as a
-- little-endian float32 BLOB by the application (SQLite has no native vector
-- type and this build is cgo-free, so no sqlite-vec). The model id and dim are
-- recorded so a model or dimension change is detectable and stale vectors can
-- be re-embedded by the app-side backfill. Rows are written only when an
-- embedder is configured; with none, this table stays empty and lexical recall
-- is unaffected.
CREATE TABLE IF NOT EXISTS memory_vectors (
	memory_id TEXT PRIMARY KEY REFERENCES memories(id) ON DELETE CASCADE,
	model TEXT NOT NULL,
	dim INTEGER NOT NULL,
	vector BLOB NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS memory_vectors;
