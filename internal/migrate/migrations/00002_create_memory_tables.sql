-- +goose Up
-- Runs after 00001, so sources(id) exists for the memory_links foreign key.
CREATE TABLE IF NOT EXISTS memories (
	id TEXT PRIMARY KEY,
	agent TEXT NOT NULL,
	kind TEXT NOT NULL,
	text TEXT NOT NULL,
	created_at TEXT NOT NULL,
	metadata_json TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS memories_agent_idx ON memories (agent);

CREATE TABLE IF NOT EXISTS memory_links (
	source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
	memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
	kind TEXT NOT NULL,
	created_at TEXT NOT NULL,
	metadata_json TEXT NOT NULL,
	PRIMARY KEY (source_id, memory_id, kind)
);

-- +goose Down
DROP TABLE IF EXISTS memory_links;
DROP INDEX IF EXISTS memories_agent_idx;
DROP TABLE IF EXISTS memories;
