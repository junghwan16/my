package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Agent turns a source and its events into memory items.
type Agent interface {
	Name() string
	Ingest(context.Context, AgentInput) (AgentOutput, error)
}

// AgentInput is the source context given to an ingest agent.
type AgentInput struct {
	Source Source
	Events []SourceEvent
}

// AgentOutput is the memory material produced by one agent.
type AgentOutput struct {
	Items []AgentItem
}

// AgentItem is a single memory candidate produced by an agent.
type AgentItem struct {
	Kind         ItemKind        `json:"kind"`
	Text         string          `json:"text"`
	MetadataJSON json.RawMessage `json:"metadata_json"`
}

// defaultConcurrency caps how many agents run at once when the caller does not
// set IngestOptions.Concurrency. Agents typically spawn external LLM processes,
// so an unbounded fan-out would exhaust local resources.
const defaultConcurrency = 4

// IngestStore is the persistence surface the ingest pipeline depends on.
type IngestStore interface {
	Sources(context.Context) ([]Source, error)
	Source(context.Context, SourceID) (Source, error)
	SourceEvents(context.Context, SourceID) ([]SourceEvent, error)
	SourceHasAgentItems(context.Context, SourceID, string) (bool, error)
	ReplaceSourceItems(context.Context, SourceID, string, []Item, []Link) error
}

// IngestResult summarizes an ingest run.
type IngestResult struct {
	Sources int
	Items   int
	Errors  int
}

// IngestOptions narrows which sources are processed and tunes the run.
type IngestOptions struct {
	SourceIDs []SourceID
	Limit     int
	// Concurrency caps simultaneously running agents. Zero means defaultConcurrency.
	Concurrency int
	// SkipExisting skips (source, agent) pairs that already produced items,
	// making re-runs cheap and resumable.
	SkipExisting bool
}

// IngestSources runs all agents for every recorded source and links produced items.
func IngestSources(
	ctx context.Context,
	store IngestStore,
	agents []Agent,
	now time.Time,
	logger *slog.Logger,
) (IngestResult, error) {
	return IngestSourcesWithOptions(ctx, store, agents, IngestOptions{}, now, logger)
}

// IngestSourcesWithOptions runs all agents for selected recorded sources.
func IngestSourcesWithOptions(
	ctx context.Context,
	store IngestStore,
	agents []Agent,
	options IngestOptions,
	now time.Time,
	logger *slog.Logger,
) (IngestResult, error) {
	logger = loggerOrDiscard(logger)
	if len(agents) == 0 {
		return IngestResult{}, errors.New("ingest requires at least one agent")
	}

	sources, err := selectIngestSources(ctx, store, options)
	if err != nil {
		return IngestResult{}, err
	}

	concurrency := options.Concurrency
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}
	sem := make(chan struct{}, concurrency)

	var result IngestResult
	for _, source := range sources {
		events, err := store.SourceEvents(ctx, source.ID)
		if err != nil {
			return IngestResult{}, err
		}
		items, agentErrors, err := ingestSource(ctx, store, source, events, agents, sem, options.SkipExisting, now, logger)
		if err != nil {
			return IngestResult{}, err
		}
		result.Sources++
		result.Items += items
		result.Errors += agentErrors
	}
	return result, nil
}

func selectIngestSources(ctx context.Context, store IngestStore, options IngestOptions) ([]Source, error) {
	var sources []Source
	if len(options.SourceIDs) > 0 {
		sources = make([]Source, 0, len(options.SourceIDs))
		for _, id := range options.SourceIDs {
			source, err := store.Source(ctx, id)
			if err != nil {
				return nil, err
			}
			sources = append(sources, source)
		}
	} else {
		var err error
		sources, err = store.Sources(ctx)
		if err != nil {
			return nil, err
		}
	}

	if options.Limit <= 0 || options.Limit >= len(sources) {
		return sources, nil
	}
	return sources[:options.Limit], nil
}

func ingestSource(
	ctx context.Context,
	store IngestStore,
	source Source,
	events []SourceEvent,
	agents []Agent,
	sem chan struct{},
	skipExisting bool,
	now time.Time,
	logger *slog.Logger,
) (int, int, error) {
	active, err := selectAgents(ctx, store, source.ID, agents, skipExisting, logger)
	if err != nil {
		return 0, 0, err
	}
	if len(active) == 0 {
		return 0, 0, nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan agentRunResult, len(active))
	for _, agent := range active {
		go func(agent Agent) {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-runCtx.Done():
				results <- agentRunResult{agent: agent.Name(), err: runCtx.Err()}
				return
			}
			output, err := agent.Ingest(runCtx, AgentInput{
				Source: source,
				Events: events,
			})
			results <- agentRunResult{
				agent:  agent.Name(),
				output: output,
				err:    err,
			}
		}(agent)
	}

	var count int
	var agentErrors int
	for range active {
		run := <-results
		if run.err != nil {
			agentErrors++
			logger.ErrorContext(ctx, "ingest agent failed",
				"source_id", source.ID,
				"agent", run.agent,
				"error", run.err,
			)
			continue
		}

		items, links := itemsFromAgentRun(source.ID, run, now)
		if err := store.ReplaceSourceItems(ctx, source.ID, run.agent, items, links); err != nil {
			return 0, agentErrors, err
		}
		count += len(items)

		logger.InfoContext(ctx, "ingested source with agent",
			"source_id", source.ID,
			"agent", run.agent,
			"items", len(items),
		)
	}
	if agentErrors == len(active) {
		return 0, agentErrors, fmt.Errorf("all ingest agents failed for source %q", source.ID)
	}
	return count, agentErrors, nil
}

// selectAgents drops agents whose items already exist for the source when
// SkipExisting is set, so re-runs only do outstanding work.
func selectAgents(
	ctx context.Context,
	store IngestStore,
	sourceID SourceID,
	agents []Agent,
	skipExisting bool,
	logger *slog.Logger,
) ([]Agent, error) {
	if !skipExisting {
		return agents, nil
	}
	active := make([]Agent, 0, len(agents))
	for _, agent := range agents {
		has, err := store.SourceHasAgentItems(ctx, sourceID, agent.Name())
		if err != nil {
			return nil, err
		}
		if has {
			logger.InfoContext(ctx, "skipped already-ingested source with agent",
				"source_id", sourceID,
				"agent", agent.Name(),
			)
			continue
		}
		active = append(active, agent)
	}
	return active, nil
}

// itemsFromAgentRun turns one agent's output into items and their source links.
func itemsFromAgentRun(sourceID SourceID, run agentRunResult, now time.Time) ([]Item, []Link) {
	items := make([]Item, 0, len(run.output.Items))
	links := make([]Link, 0, len(run.output.Items))
	for _, agentItem := range run.output.Items {
		item := itemFromAgentOutput(sourceID, run.agent, agentItem, now)
		items = append(items, item)
		links = append(links, Link{
			SourceID:     sourceID,
			ItemID:       item.ID,
			Kind:         LinkKindSourceIngest,
			CreatedAt:    now,
			MetadataJSON: mustMarshalJSON(map[string]string{"agent": run.agent}),
		})
	}
	return items, links
}

type agentRunResult struct {
	agent  string
	output AgentOutput
	err    error
}

func itemFromAgentOutput(sourceID SourceID, agent string, agentItem AgentItem, now time.Time) Item {
	if agentItem.Kind == "" {
		agentItem.Kind = ItemKindSummary
	}
	if len(agentItem.MetadataJSON) == 0 {
		agentItem.MetadataJSON = jsonObject()
	}

	return Item{
		ID:           deterministicItemID(sourceID, agent, agentItem.Kind, agentItem.Text),
		Agent:        agent,
		Kind:         agentItem.Kind,
		Text:         agentItem.Text,
		CreatedAt:    now,
		MetadataJSON: agentItem.MetadataJSON,
	}
}

func deterministicItemID(sourceID SourceID, agent string, kind ItemKind, text string) ItemID {
	h := sha256.New()
	_, _ = h.Write([]byte(sourceID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(agent))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(text))
	return ItemID("memory_item:" + hex.EncodeToString(h.Sum(nil)))
}
