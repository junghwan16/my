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
	"github.com/junghwan16/my/internal/migrate"
	"github.com/junghwan16/my/internal/source"
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

// withStores opens the SQLite database, brings its schema up to date through the
// migration ledger, and hands ready stores to fn, guaranteeing the database is
// closed afterwards.
func withStores(ctx context.Context, path string, fn func(*source.Store, *memory.Store) error) (err error) {
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close sqlite database: %w", closeErr)
		}
	}()
	if err = migrate.Apply(ctx, db, path); err != nil {
		return err
	}
	return fn(source.NewStore(db), memory.NewStore(db))
}

func runMemoryImport(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, now time.Time) error {
	config, err := parseMemoryImportConfig(args, stderr)
	if err != nil {
		return err
	}

	return withStores(ctx, config.storePath, func(sources *source.Store, _ *memory.Store) error {
		logger := slog.New(slog.NewTextHandler(stderr, nil))
		result, err := source.NewImporter(sources, logger).Import(ctx, config.from, now)
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

	return withStores(ctx, config.storePath, func(sources *source.Store, memories *memory.Store) error {
		logger := slog.New(slog.NewTextHandler(stderr, nil))
		ingester := memory.NewIngester(sources, memories, config.agents, logger)
		result, err := ingester.Ingest(ctx, config.options, now)
		if err != nil {
			return err
		}

		if _, err := fmt.Fprintf(
			stdout,
			"ingested %d source(s), created %d memories, failed %d agent run(s)\n",
			result.Sources,
			result.Memories,
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
			agents:    memory.DefaultAgents(),
			options:   options,
		}, nil
	}

	agents := make([]memory.Agent, 0, len(agentSpecs))
	for _, spec := range agentSpecs {
		agent, err := memory.ParseAgentSpec(spec)
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

	options.SourceIDs = make([]source.SourceID, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		options.SourceIDs = append(options.SourceIDs, source.SourceID(id))
	}
	return options
}

type repeatedFlag []string

func (f *repeatedFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *repeatedFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func defaultStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "my", "memory", "my.db"), nil
}
