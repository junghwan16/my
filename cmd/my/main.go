package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/junghwan16/my/internal/memory"
	"github.com/junghwan16/my/internal/storage"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, time.Now().UTC()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, now time.Time) error {
	if len(args) < 2 || args[0] != "memory" {
		return errors.New("usage: my memory <import|ingest>")
	}

	switch args[1] {
	case "import":
		return runMemoryImport(ctx, args, stdout, stderr, now)
	case "ingest":
		return runMemoryIngest(ctx, args, stdout, stderr, now)
	default:
		return errors.New("usage: my memory <import|ingest>")
	}
}

// withStore opens the SQLite store, migrates it, and hands a ready Store to fn,
// guaranteeing the database is closed afterwards.
func withStore(ctx context.Context, path string, fn func(*memory.Store) error) (err error) {
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close sqlite database: %w", closeErr)
		}
	}()
	if err = memory.Migrate(ctx, db); err != nil {
		return err
	}
	return fn(memory.NewStore(db))
}

func runMemoryImport(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, now time.Time) error {
	config, err := parseMemoryImportConfig(args, stderr)
	if err != nil {
		return err
	}

	return withStore(ctx, config.storePath, func(store *memory.Store) error {
		logger := slog.New(slog.NewTextHandler(stderr, nil))
		result, err := memory.ImportSessions(ctx, store, config.from, now, logger)
		if err != nil {
			return err
		}

		if _, err := fmt.Fprintf(
			stdout,
			"imported %d session(s), skipped %d unsupported file(s)\n",
			result.Imported,
			result.Skipped,
		); err != nil {
			return fmt.Errorf("write import summary: %w", err)
		}
		return nil
	})
}

func runMemoryIngest(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, now time.Time) error {
	config, err := parseMemoryIngestConfig(args, stderr)
	if err != nil {
		return err
	}

	return withStore(ctx, config.storePath, func(store *memory.Store) error {
		logger := slog.New(slog.NewTextHandler(stderr, nil))
		result, err := memory.IngestSourcesWithOptions(ctx, store, config.agents, config.options, now, logger)
		if err != nil {
			return err
		}

		if _, err := fmt.Fprintf(
			stdout,
			"ingested %d source(s), created %d item(s), failed %d agent run(s)\n",
			result.Sources,
			result.Items,
			result.Errors,
		); err != nil {
			return fmt.Errorf("write ingest summary: %w", err)
		}
		return nil
	})
}

type memoryImportConfig struct {
	from      string
	storePath string
}

type memoryIngestConfig struct {
	storePath string
	agents    []memory.Agent
	options   memory.IngestOptions
}

func parseMemoryImportConfig(args []string, stderr io.Writer) (memoryImportConfig, error) {
	if len(args) < 2 || args[0] != "memory" || args[1] != "import" {
		return memoryImportConfig{}, errors.New("usage: my memory import --from <path> --store <sqlite-db>")
	}

	flags := flag.NewFlagSet("memory import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	from := flags.String("from", "", "Codex sessions file or directory")
	storePath := flags.String("store", "", "SQLite memory store path")
	if err := flags.Parse(args[2:]); err != nil {
		return memoryImportConfig{}, err
	}
	if *from == "" {
		return memoryImportConfig{}, errors.New("missing --from")
	}
	if *storePath == "" {
		defaultPath, err := defaultStorePath()
		if err != nil {
			return memoryImportConfig{}, err
		}
		*storePath = defaultPath
	}

	return memoryImportConfig{from: *from, storePath: *storePath}, nil
}

func parseMemoryIngestConfig(args []string, stderr io.Writer) (memoryIngestConfig, error) {
	if len(args) < 2 || args[0] != "memory" || args[1] != "ingest" {
		return memoryIngestConfig{}, errors.New("usage: my memory ingest --agent <name=command[,arg...]> --store <sqlite-db>")
	}

	flags := flag.NewFlagSet("memory ingest", flag.ContinueOnError)
	flags.SetOutput(stderr)
	storePath := flags.String("store", "", "SQLite memory store path")
	limit := flags.Int("limit", 0, "Maximum number of sources to ingest")
	concurrency := flags.Int("concurrency", 0, "Maximum agents to run at once (0 = default)")
	skipExisting := flags.Bool("skip-existing", false, "Skip sources already ingested by an agent")
	var agentSpecs repeatedFlag
	var sourceIDs repeatedFlag
	flags.Var(&agentSpecs, "agent", "Agent command as name=command[,arg...]")
	flags.Var(&sourceIDs, "source-id", "Source ID to ingest; repeatable")
	if err := flags.Parse(args[2:]); err != nil {
		return memoryIngestConfig{}, err
	}
	if *storePath == "" {
		defaultPath, err := defaultStorePath()
		if err != nil {
			return memoryIngestConfig{}, err
		}
		*storePath = defaultPath
	}

	options := newIngestOptions(sourceIDs, *limit, *concurrency, *skipExisting)
	if len(agentSpecs) == 0 {
		return memoryIngestConfig{
			storePath: *storePath,
			agents:    defaultIngestAgents(),
			options:   options,
		}, nil
	}

	agents := make([]memory.Agent, 0, len(agentSpecs))
	for _, spec := range agentSpecs {
		agent, err := parseAgentSpec(spec)
		if err != nil {
			return memoryIngestConfig{}, err
		}
		agents = append(agents, agent)
	}
	return memoryIngestConfig{
		storePath: *storePath,
		agents:    agents,
		options:   options,
	}, nil
}

func newIngestOptions(sourceIDs []string, limit int, concurrency int, skipExisting bool) memory.IngestOptions {
	options := memory.IngestOptions{
		Limit:        limit,
		Concurrency:  concurrency,
		SkipExisting: skipExisting,
	}
	if len(sourceIDs) == 0 {
		return options
	}

	options.SourceIDs = make([]memory.SourceID, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		options.SourceIDs = append(options.SourceIDs, memory.SourceID(id))
	}
	return options
}

func defaultIngestAgents() []memory.Agent {
	return []memory.Agent{
		memory.NewCommandAgent("claude", "claude", "-p", "--no-session-persistence", "--disallowedTools=Bash,Edit,Write"),
		memory.NewCommandAgent(
			"codex",
			"codex",
			"--ask-for-approval",
			"never",
			"exec",
			"--sandbox",
			"read-only",
			"--skip-git-repo-check",
		),
		memory.NewCommandAgent("pi", "pi", "-p", "--no-tools", "--no-session"),
	}
}

type repeatedFlag []string

func (f *repeatedFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *repeatedFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func parseAgentSpec(spec string) (memory.CommandAgent, error) {
	name, commandSpec, ok := strings.Cut(spec, "=")
	if !ok || name == "" || commandSpec == "" {
		return memory.CommandAgent{}, fmt.Errorf("invalid agent spec %q", spec)
	}

	parts := strings.Split(commandSpec, ",")
	for _, part := range parts {
		if part == "" {
			return memory.CommandAgent{}, fmt.Errorf("invalid agent spec %q", spec)
		}
	}
	return memory.NewCommandAgent(name, parts[0], parts[1:]...), nil
}

func defaultStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "my", "memory", "my.db"), nil
}
