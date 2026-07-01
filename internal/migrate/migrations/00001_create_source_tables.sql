-- +goose Up
-- CREATE ... IF NOT EXISTS keeps this a no-op baseline on databases created
-- before migrations existed (their tables are already present).
CREATE TABLE IF NOT EXISTS sources (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	uri TEXT NOT NULL,
	content_sha256 TEXT NOT NULL,
	scope_kind TEXT NOT NULL,
	scope_value TEXT NOT NULL,
	started_at TEXT,
	ended_at TEXT,
	recorded_at TEXT NOT NULL,
	metadata_json TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS sources_kind_hash_idx
	ON sources (kind, content_sha256);

CREATE TABLE IF NOT EXISTS source_events (
	source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
	event_index INTEGER NOT NULL,
	line INTEGER NOT NULL,
	at TEXT,
	type TEXT NOT NULL,
	turn_id TEXT NOT NULL DEFAULT '',
	role TEXT NOT NULL DEFAULT '',
	text TEXT NOT NULL DEFAULT '',
	payload_json TEXT NOT NULL,
	raw_json TEXT NOT NULL,
	PRIMARY KEY (source_id, event_index)
);

-- +goose Down
DROP TABLE IF EXISTS source_events;
DROP INDEX IF EXISTS sources_kind_hash_idx;
DROP TABLE IF EXISTS sources;
