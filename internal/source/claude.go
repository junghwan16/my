package source

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/junghwan16/gieok/internal/jsonutil"
)

/*
Claude Code session JSONL format

Claude Code stores project sessions under ~/.claude/projects/<project>/*.jsonl.
Each line is a top-level record with a "type" field. Message records commonly
carry sessionId, cwd, timestamp, and message.role/message.content.
*/

// claudeFormat parses Claude Code session JSONL files.
type claudeFormat struct{}

var _ SessionFormat = claudeFormat{}

func (claudeFormat) Kind() SourceKind { return SourceKindClaudeCodeSession }

func (claudeFormat) Sniff(raw []byte) bool { return looksLikeClaudeCodeSession(raw) }

func (claudeFormat) Parse(raw []byte) (ParsedSession, error) {
	events, meta, err := parseClaudeCodeEvents(bytes.NewReader(raw))
	if err != nil {
		return ParsedSession{}, err
	}

	return ParsedSession{
		Events:    events,
		Scope:     Scope{Kind: ScopeKindWorkspace, Value: meta.CWD},
		StartedAt: firstEventTime(events),
		Metadata:  jsonutil.MustMarshal(meta),
	}, nil
}

func looksLikeClaudeCodeSession(raw []byte) bool {
	record := firstJSONLRecord(raw)
	if len(record) == 0 {
		return false
	}

	var event claudeCodeRecord
	if err := json.Unmarshal(record, &event); err != nil {
		return false
	}
	return event.Type != ""
}

type claudeCodeMeta struct {
	SessionID string `json:"session_id,omitempty"`
	CWD       string `json:"cwd,omitempty"`
	Version   string `json:"version,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
}

func (m *claudeCodeMeta) merge(update claudeCodeMeta) {
	if update.SessionID != "" {
		m.SessionID = update.SessionID
	}
	if update.CWD != "" {
		m.CWD = update.CWD
	}
	if update.Version != "" {
		m.Version = update.Version
	}
	if update.GitBranch != "" {
		m.GitBranch = update.GitBranch
	}
}

type claudeCodeRecord struct {
	Type       string            `json:"type"`
	SessionID  string            `json:"sessionId"`
	Timestamp  string            `json:"timestamp"`
	CWD        string            `json:"cwd"`
	Version    string            `json:"version"`
	GitBranch  string            `json:"gitBranch"`
	Message    claudeCodeMessage `json:"message"`
	Attachment json.RawMessage   `json:"attachment"`
}

type claudeCodeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type claudeCodeContentPart struct {
	Text string `json:"text"`
}

func parseClaudeCodeEvents(r io.Reader) ([]SourceEvent, claudeCodeMeta, error) {
	reader := bufio.NewReader(r)
	var events []SourceEvent
	var meta claudeCodeMeta
	lineNumber := 0

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			lineNumber++
			event, update, parseErr := parseClaudeCodeLine(lineNumber, line)
			if parseErr != nil {
				return nil, claudeCodeMeta{}, parseErr
			}
			meta.merge(update)
			events = append(events, event)
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			break
		}
		return nil, claudeCodeMeta{}, fmt.Errorf("read claude code session: %w", err)
	}
	if len(events) == 0 {
		return nil, claudeCodeMeta{}, errors.New("empty claude code session")
	}
	return events, meta, nil
}

func parseClaudeCodeLine(lineNumber int, line []byte) (SourceEvent, claudeCodeMeta, error) {
	raw := bytes.TrimSpace(line)
	if len(raw) == 0 {
		return SourceEvent{}, claudeCodeMeta{}, nil
	}

	var record claudeCodeRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return SourceEvent{}, claudeCodeMeta{}, fmt.Errorf("parse claude code session line %d: %w", lineNumber, err)
	}
	if record.Type == "" {
		return SourceEvent{}, claudeCodeMeta{}, fmt.Errorf("parse claude code session line %d: missing type", lineNumber)
	}

	return SourceEvent{
			Line:        lineNumber,
			At:          parseTimestamp(record.Timestamp),
			Type:        record.Type,
			Role:        record.Message.Role,
			Text:        claudeCodeMessageText(record.Message.Content),
			PayloadJSON: jsonutil.Clone(raw),
			RawJSON:     jsonutil.Clone(raw),
		}, claudeCodeMeta{
			SessionID: record.SessionID,
			CWD:       record.CWD,
			Version:   record.Version,
			GitBranch: record.GitBranch,
		}, nil
}

func claudeCodeMessageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var parts []claudeCodeContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	return contentPartText(parts)
}

func contentPartText(parts []claudeCodeContentPart) string {
	var text string
	for _, part := range parts {
		if part.Text == "" {
			continue
		}
		if text != "" {
			text += "\n"
		}
		text += part.Text
	}
	return text
}
