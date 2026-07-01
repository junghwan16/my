package sources

import (
	"time"

	"github.com/uptrace/bun"

	"github.com/junghwan16/gieok/internal/jsonutil"
)

// These row models map onto the sources and source_events tables. The schema
// that creates and evolves them lives in internal/migrate; keep the
// column tags here in sync with those migrations.

type sourceRow struct {
	bun.BaseModel `bun:"table:sources"`

	ID            string    `bun:"id,pk"`
	Kind          string    `bun:"kind,notnull"`
	URI           string    `bun:"uri,notnull"`
	ContentSHA256 string    `bun:"content_sha256,notnull"`
	ScopeKind     string    `bun:"scope_kind,notnull"`
	ScopeValue    string    `bun:"scope_value,notnull"`
	StartedAt     time.Time `bun:"started_at,nullzero"`
	EndedAt       time.Time `bun:"ended_at,nullzero"`
	ImportedAt    time.Time `bun:"imported_at,notnull"`
	MetadataJSON  string    `bun:"metadata_json,notnull"`
}

type sourceEventRow struct {
	bun.BaseModel `bun:"table:source_events"`

	SourceID    string    `bun:"source_id,pk"`
	Index       int       `bun:"event_index,pk"`
	Line        int       `bun:"line,notnull"`
	At          time.Time `bun:"at,nullzero"`
	Type        string    `bun:"type,notnull"`
	TurnID      string    `bun:"turn_id,notnull"`
	Role        string    `bun:"role,notnull"`
	Text        string    `bun:"text,notnull"`
	PayloadJSON string    `bun:"payload_json,notnull"`
	RawJSON     string    `bun:"raw_json,notnull"`
}

func newSourceRow(source Source) *sourceRow {
	return &sourceRow{
		ID:            string(source.ID),
		Kind:          string(source.Kind),
		URI:           source.URI,
		ContentSHA256: source.ContentSHA256,
		ScopeKind:     string(source.Scope.Kind),
		ScopeValue:    source.Scope.Value,
		StartedAt:     source.StartedAt,
		EndedAt:       source.EndedAt,
		ImportedAt:    source.ImportedAt,
		MetadataJSON:  string(source.MetadataJSON),
	}
}

func (r sourceRow) toSource() Source {
	return Source{
		ID:            SourceID(r.ID),
		Kind:          SourceKind(r.Kind),
		URI:           r.URI,
		ContentSHA256: r.ContentSHA256,
		Scope: Scope{
			Kind:  ScopeKind(r.ScopeKind),
			Value: r.ScopeValue,
		},
		StartedAt:    r.StartedAt,
		EndedAt:      r.EndedAt,
		ImportedAt:   r.ImportedAt,
		MetadataJSON: []byte(r.MetadataJSON),
	}
}

func newSourceEventRow(event SourceEvent) sourceEventRow {
	payload := event.PayloadJSON
	if len(payload) == 0 {
		payload = jsonutil.EmptyObject()
	}
	raw := event.RawJSON
	if len(raw) == 0 {
		raw = jsonutil.EmptyObject()
	}

	return sourceEventRow{
		SourceID:    string(event.SourceID),
		Index:       event.Index,
		Line:        event.Line,
		At:          event.At,
		Type:        event.Type,
		TurnID:      event.TurnID,
		Role:        event.Role,
		Text:        event.Text,
		PayloadJSON: string(payload),
		RawJSON:     string(raw),
	}
}

func (r sourceEventRow) toSourceEvent() SourceEvent {
	return SourceEvent{
		SourceID:    SourceID(r.SourceID),
		Index:       r.Index,
		Line:        r.Line,
		At:          r.At,
		Type:        r.Type,
		TurnID:      r.TurnID,
		Role:        r.Role,
		Text:        r.Text,
		PayloadJSON: []byte(r.PayloadJSON),
		RawJSON:     []byte(r.RawJSON),
	}
}
