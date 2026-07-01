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

	"github.com/junghwan16/gieok/internal/jsonutil"
	"github.com/junghwan16/gieok/internal/source"
)

// SourceReader supplies the sources an ingest run reads. *source.Store satisfies it.
type SourceReader interface {
	Sources(context.Context) ([]source.Source, error)
	Source(context.Context, source.SourceID) (source.Source, error)
	SourceEvents(context.Context, source.SourceID) ([]source.SourceEvent, error)
}

// MemoryWriter persists the memories an ingest run produces. *Store satisfies it.
type MemoryWriter interface {
	SourceHasAgentMemories(context.Context, source.SourceID, string) (bool, error)
	ReplaceSourceMemories(context.Context, source.SourceID, string, []Memory, []Link) error
}

// MemoryStore reads and writes memories. *Store satisfies it. Ingest writes new
// memories through MemoryWriter and reads through MemoryReader to recall
// existing memory an agent should build on rather than ingesting each source
// in isolation.
type MemoryStore interface {
	MemoryWriter
	MemoryReader
}

// Agent turns a source and its events into memories.
type Agent interface {
	Name() string
	Ingest(context.Context, AgentInput) (AgentOutput, error)
}

// AgentInput is the source context given to an ingest agent.
type AgentInput struct {
	Source source.Source
	Events []source.SourceEvent
	// RelatedMemories is existing memory already recalled as relevant to this
	// source (same scope, recalled by the source's own sampled content),
	// excluding any memory already linked to this source. It lets an agent
	// connect a new memory to what it already knows instead of summarizing
	// the source in isolation.
	RelatedMemories []Recollection
}

// AgentOutput is the memory material produced by one agent.
type AgentOutput struct {
	Memories []AgentMemory
}

// AgentMemory is a single memory candidate produced by an agent.
type AgentMemory struct {
	Kind         MemoryKind      `json:"kind"`
	Text         string          `json:"text"`
	MetadataJSON json.RawMessage `json:"metadata_json"`
}

// defaultConcurrency caps how many agents run at once when the caller does not
// set IngestOptions.Concurrency. Agents typically spawn external LLM processes,
// so an unbounded fan-out would exhaust local resources.
const defaultConcurrency = 4

// relatedMemoryLimit caps how much existing memory is recalled as context for
// one source, bounding both the recall cost and the prompt size.
const relatedMemoryLimit = 5

// IngestOptions narrows which sources are processed and tunes the run.
type IngestOptions struct {
	SourceIDs []source.SourceID
	Limit     int
	// Concurrency caps simultaneously running agents. Zero means defaultConcurrency.
	Concurrency int
	// SkipExisting skips (source, agent) pairs that already produced memories,
	// making re-runs cheap and resumable.
	SkipExisting bool
}

// IngestResult summarizes an ingest run.
type IngestResult struct {
	Sources  int
	Memories int
	Errors   int
}

// Ingester runs agents over recorded sources and links the memories they produce.
type Ingester struct {
	sources  SourceReader
	memories MemoryStore
	related  *Recaller
	agents   []Agent
	logger   *slog.Logger
}

// NewIngester wires an Ingester to its source reader, memory store, and agents.
func NewIngester(sources SourceReader, memories MemoryStore, agents []Agent, logger *slog.Logger) *Ingester {
	return &Ingester{
		sources:  sources,
		memories: memories,
		related:  NewRecaller(memories),
		agents:   agents,
		logger:   loggerOrDiscard(logger),
	}
}

// Ingest runs every agent for the selected sources and links produced memories.
func (in *Ingester) Ingest(ctx context.Context, options IngestOptions, now time.Time) (IngestResult, error) {
	if len(in.agents) == 0 {
		return IngestResult{}, errors.New("ingest requires at least one agent")
	}

	sources, err := in.selectSources(ctx, options)
	if err != nil {
		return IngestResult{}, err
	}

	concurrency := options.Concurrency
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}
	sem := make(chan struct{}, concurrency)

	var result IngestResult
	for _, src := range sources {
		events, err := in.sources.SourceEvents(ctx, src.ID)
		if err != nil {
			return IngestResult{}, err
		}
		memories, agentErrors, err := in.ingestSource(ctx, src, events, sem, options.SkipExisting, now)
		if err != nil {
			return IngestResult{}, err
		}
		result.Sources++
		result.Memories += memories
		result.Errors += agentErrors
	}
	return result, nil
}

func (in *Ingester) selectSources(ctx context.Context, options IngestOptions) ([]source.Source, error) {
	var sources []source.Source
	if len(options.SourceIDs) > 0 {
		sources = make([]source.Source, 0, len(options.SourceIDs))
		for _, id := range options.SourceIDs {
			src, err := in.sources.Source(ctx, id)
			if err != nil {
				return nil, err
			}
			sources = append(sources, src)
		}
	} else {
		var err error
		sources, err = in.sources.Sources(ctx)
		if err != nil {
			return nil, err
		}
	}

	if options.Limit <= 0 || options.Limit >= len(sources) {
		return sources, nil
	}
	return sources[:options.Limit], nil
}

func (in *Ingester) ingestSource(
	ctx context.Context,
	src source.Source,
	events []source.SourceEvent,
	sem chan struct{},
	skipExisting bool,
	now time.Time,
) (int, int, error) {
	active, err := in.selectAgents(ctx, src.ID, skipExisting)
	if err != nil {
		return 0, 0, err
	}
	if len(active) == 0 {
		return 0, 0, nil
	}

	related, err := in.recallRelatedMemories(ctx, src, events)
	if err != nil {
		return 0, 0, err
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
			output, err := agent.Ingest(runCtx, AgentInput{Source: src, Events: events, RelatedMemories: related})
			results <- agentRunResult{agent: agent.Name(), output: output, err: err}
		}(agent)
	}

	var count int
	var agentErrors int
	for range active {
		run := <-results
		if run.err != nil {
			agentErrors++
			in.logger.ErrorContext(ctx, "ingest agent failed",
				"source_id", src.ID,
				"agent", run.agent,
				"error", run.err,
			)
			continue
		}

		memories, links := memoriesFromAgentRun(src.ID, run, now)
		if err := in.memories.ReplaceSourceMemories(ctx, src.ID, run.agent, memories, links); err != nil {
			return 0, agentErrors, err
		}
		count += len(memories)

		in.logger.InfoContext(ctx, "ingested source with agent",
			"source_id", src.ID,
			"agent", run.agent,
			"memories", len(memories),
		)
	}
	if agentErrors == len(active) {
		return 0, agentErrors, fmt.Errorf("all ingest agents failed for source %q", src.ID)
	}
	return count, agentErrors, nil
}

// recallRelatedMemories finds existing memory relevant to a source before any
// agent ingests it, so ingestion can connect new memory to what is already
// known instead of treating every source as a blank slate. It recalls within
// the source's own scope using the source's sampled content as the query, and
// excludes any memory already linked to this exact source (its own prior
// ingest, on a re-run) since that is not "other" existing knowledge.
func (in *Ingester) recallRelatedMemories(
	ctx context.Context,
	src source.Source,
	events []source.SourceEvent,
) ([]Recollection, error) {
	query := relatedMemoryQuery(events)
	if query == "" {
		return nil, nil
	}

	recalled, err := in.related.Recollect(ctx, query, src.Scope.Value, relatedMemoryLimit)
	if err != nil {
		return nil, err
	}

	related := make([]Recollection, 0, len(recalled))
	for _, recollection := range recalled {
		if recollectionHasSource(recollection, src.ID) {
			continue
		}
		related = append(related, recollection)
	}
	return related, nil
}

func recollectionHasSource(recollection Recollection, sourceID source.SourceID) bool {
	for _, ref := range recollection.Sources {
		if ref.ID == sourceID {
			return true
		}
	}
	return false
}

// selectAgents drops agents whose memories already exist for the source when
// skipExisting is set, so re-runs only do outstanding work.
func (in *Ingester) selectAgents(ctx context.Context, sourceID source.SourceID, skipExisting bool) ([]Agent, error) {
	if !skipExisting {
		return in.agents, nil
	}
	active := make([]Agent, 0, len(in.agents))
	for _, agent := range in.agents {
		has, err := in.memories.SourceHasAgentMemories(ctx, sourceID, agent.Name())
		if err != nil {
			return nil, err
		}
		if has {
			in.logger.InfoContext(ctx, "skipped already-ingested source with agent",
				"source_id", sourceID,
				"agent", agent.Name(),
			)
			continue
		}
		active = append(active, agent)
	}
	return active, nil
}

// memoriesFromAgentRun turns one agent's output into memories and their source links.
func memoriesFromAgentRun(sourceID source.SourceID, run agentRunResult, now time.Time) ([]Memory, []Link) {
	memories := make([]Memory, 0, len(run.output.Memories))
	links := make([]Link, 0, len(run.output.Memories))
	for _, agentMemory := range run.output.Memories {
		mem := memoryFromAgentOutput(sourceID, run.agent, agentMemory, now)
		memories = append(memories, mem)
		links = append(links, Link{
			SourceID:     sourceID,
			MemoryID:     mem.ID,
			Kind:         LinkKindSourceIngest,
			CreatedAt:    now,
			MetadataJSON: jsonutil.MustMarshal(map[string]string{"agent": run.agent}),
		})
	}
	return memories, links
}

type agentRunResult struct {
	agent  string
	output AgentOutput
	err    error
}

func memoryFromAgentOutput(sourceID source.SourceID, agent string, agentMemory AgentMemory, now time.Time) Memory {
	if agentMemory.Kind == "" {
		agentMemory.Kind = MemoryKindSummary
	}
	if len(agentMemory.MetadataJSON) == 0 {
		agentMemory.MetadataJSON = jsonutil.EmptyObject()
	}

	return Memory{
		ID:           deterministicMemoryID(sourceID, agent, agentMemory.Kind, agentMemory.Text),
		Agent:        agent,
		Kind:         agentMemory.Kind,
		Text:         agentMemory.Text,
		CreatedAt:    now,
		MetadataJSON: agentMemory.MetadataJSON,
	}
}

func deterministicMemoryID(sourceID source.SourceID, agent string, kind MemoryKind, text string) MemoryID {
	h := sha256.New()
	_, _ = h.Write([]byte(sourceID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(agent))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(text))
	return MemoryID("memory:" + hex.EncodeToString(h.Sum(nil)))
}

func loggerOrDiscard(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.New(slog.DiscardHandler)
}
