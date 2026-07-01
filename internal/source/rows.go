package sources

import (
	"time"

	"github.com/junghwan16/gieok/internal/jsonutil"
)

// These row models map onto the sources and source_events tables. The schema
// that creates and evolves them lives in internal/migrate; keep the
// column tags here in sync with those migrations.

type sourceRow struct {
	ID            string
	Kind          string
	URI           string
	ContentSHA256 string
	ScopeKind     string
	ScopeValue    string
	StartedAt     time.Time
	EndedAt       time.Time
	ImportedAt    time.Time
	MetadataJSON  string
}

type sourceEventRow struct {
	SourceID    string
	Index       int
	Line        int
	At          time.Time
	Type        string
	TurnID      string
	Role        string
	Text        string
	PayloadJSON string
	RawJSON     string
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
