// Package jsonutil holds small JSON helpers shared by the source and memory
// stores: an empty-object literal for NOT NULL metadata columns, a panic-on-
// error marshaler for statically-encodable values, and a defensive byte copier.
package jsonutil

import (
	"encoding/json"
	"fmt"
)

// EmptyObject is the canonical empty JSON object. It backfills NOT NULL metadata
// columns when a record carries no metadata of its own.
func EmptyObject() json.RawMessage {
	return json.RawMessage(`{}`)
}

// MustMarshal marshals values that are statically known to be encodable (plain
// structs and string maps). A failure indicates a programming error, so it
// panics rather than silently dropping data, mirroring regexp.MustCompile.
func MustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("marshal json: %v", err))
	}
	return b
}

// Clone returns an independent copy of raw so the result never aliases a reused
// decode buffer.
func Clone(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	cloned := make([]byte, len(raw))
	copy(cloned, raw)
	return cloned
}
