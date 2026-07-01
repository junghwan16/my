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

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/junghwan16/my/internal/mcp"
	"github.com/junghwan16/my/internal/memory"
	"github.com/junghwan16/my/internal/migrate"
	"github.com/junghwan16/my/internal/source"
	"github.com/junghwan16/my/internal/storage"
	"github.com/junghwan16/my/internal/tokenize"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, time.Now().UTC()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, now time.Time) error {
	if len(args) < 1 {
		return errUsage
	}

	switch args[0] {
	case "memory":
		return runMemory(ctx, args, stdout, stderr, now)
	case "mcp":
		return runMCP(ctx, args, stderr)
	default:
		return errUsage
	}
}

// errUsage is the top-level usage error.
var errUsage = errors.New("usage: my <memory|mcp>")

func runMemory(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, now time.Time) error {
	if len(args) < 2 {
		return errors.New("usage: my memory <import|ingest|recall>")
	}

	switch args[1] {
	case "import":
		return runMemoryImport(ctx, args, stdout, stderr, now)
	case "ingest":
		return runMemoryIngest(ctx, args, stdout, stderr, now)
	case "recall":
		return runMemoryRecall(ctx, args, stdout, stderr)
	default:
		return errors.New("usage: my memory <import|ingest|recall>")
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
	tokenizer, err := tokenize.NewKorean()
	if err != nil {
		return fmt.Errorf("build korean tokenizer: %w", err)
	}
	memories := memory.NewStore(db, tokenizer)
	if err = memories.EnsureFTSIndexed(ctx); err != nil {
		return err
	}
	return fn(source.NewStore(db), memories)
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

func runMemoryRecall(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	config, err := parseMemoryRecallConfig(args, stderr)
	if err != nil {
		return err
	}

	return withStores(ctx, config.storePath, func(_ *source.Store, memories *memory.Store) error {
		recollections, err := memory.NewRecaller(memories).Recollect(ctx, config.task, config.scope, config.limit)
		if err != nil {
			return err
		}
		if config.json {
			return writeRecallJSON(stdout, recollections)
		}
		return writeRecallText(stdout, recollections)
	})
}

type memoryRecallConfig struct {
	storePath string
	task      string
	scope     string
	limit     int
	json      bool
}

func parseMemoryRecallConfig(args []string, stderr io.Writer) (memoryRecallConfig, error) {
	if len(args) < 2 || args[0] != "memory" || args[1] != "recall" {
		return memoryRecallConfig{}, errors.New("usage: my memory recall [task] [--scope <value>] [--all-scopes] [--limit <n>] [--json] [--store <sqlite-db>]")
	}

	flags := flag.NewFlagSet("memory recall", flag.ContinueOnError)
	flags.SetOutput(stderr)
	storePath := flags.String("store", "", "SQLite memory store path")
	task := flags.String("task", "", "Task text to recall relevant memory for")
	scope := flags.String("scope", "", "Workspace scope to recall within (default: current directory)")
	allScopes := flags.Bool("all-scopes", false, "Recall across every scope instead of the current workspace")
	limit := flags.Int("limit", 0, "Maximum number of memories to return (0 = default)")
	asJSON := flags.Bool("json", false, "Emit the recall result as JSON")

	// Task text may appear positionally before or after flags, so reorder the
	// args to put flags first. Without this, Go's flag package would stop at a
	// leading positional and silently ignore every flag after it.
	reordered, positional := partitionRecallArgs(args[2:], boolRecallFlags())
	if err := flags.Parse(reordered); err != nil {
		return memoryRecallConfig{}, err
	}
	positional = append(positional, flags.Args()...)

	taskText, err := recallTaskText(*task, positional)
	if err != nil {
		return memoryRecallConfig{}, err
	}

	resolvedScope, err := recallScope(*scope, *allScopes)
	if err != nil {
		return memoryRecallConfig{}, err
	}

	if *storePath == "" {
		defaultPath, err := defaultStorePath()
		if err != nil {
			return memoryRecallConfig{}, err
		}
		*storePath = defaultPath
	}

	return memoryRecallConfig{
		storePath: *storePath,
		task:      taskText,
		scope:     resolvedScope,
		limit:     *limit,
		json:      *asJSON,
	}, nil
}

// boolRecallFlags names the recall flags that take no value, so the arg
// partitioner knows the following token is not their value.
func boolRecallFlags() map[string]bool {
	return map[string]bool{"all-scopes": true, "json": true}
}

// partitionRecallArgs splits recall args into flag args (returned first, in
// order) and positional task words. A value flag consumes the next token as its
// value; a bool flag or a "--flag=value" token stands alone. This lets the task
// appear before or after flags without the flag package stopping early.
func partitionRecallArgs(args []string, boolFlags map[string]bool) (flags, positional []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, hasValue := flagName(arg)
		if name == "" {
			positional = append(positional, arg)
			continue
		}
		flags = append(flags, arg)
		if hasValue || boolFlags[name] {
			continue
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return flags, positional
}

// flagName returns the flag name for an arg and whether it already carries an
// inline "=value". A non-flag arg returns an empty name.
func flagName(arg string) (name string, hasInlineValue bool) {
	if !strings.HasPrefix(arg, "-") || arg == "-" || arg == "--" {
		return "", false
	}
	trimmed := strings.TrimLeft(arg, "-")
	if eq := strings.IndexByte(trimmed, '='); eq >= 0 {
		return trimmed[:eq], true
	}
	return trimmed, false
}

// errTaskConflict reports that task text was given both positionally and via --task.
var errTaskConflict = errors.New("pass task text either positionally or with --task, not both")

// recallTaskText merges the positional task words and the --task flag into one
// task string. Either form is accepted, but not both, so the command fails
// clearly instead of silently ignoring one.
func recallTaskText(taskFlag string, positional []string) (string, error) {
	joined := strings.TrimSpace(strings.Join(positional, " "))
	taskFlag = strings.TrimSpace(taskFlag)
	if joined != "" && taskFlag != "" {
		return "", errTaskConflict
	}
	if taskFlag != "" {
		return taskFlag, nil
	}
	return joined, nil
}

// errScopeConflict reports that --scope and --all-scopes were combined.
var errScopeConflict = errors.New("pass either --scope or --all-scopes, not both")

// recallScope resolves the scope to recall within: an explicit --scope, every
// scope with --all-scopes, or the current working directory by default (which
// is the scope_value import records for a workspace session).
func recallScope(scope string, allScopes bool) (string, error) {
	scope = strings.TrimSpace(scope)
	if allScopes {
		if scope != "" {
			return "", errScopeConflict
		}
		return "", nil
	}
	if scope != "" {
		return scope, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current workspace scope: %w", err)
	}
	return cwd, nil
}

// runMCP serves the memory recall tool over MCP on stdio. It parses an optional
// serve subcommand and --store flag, then blocks serving until the client
// disconnects or ctx is cancelled.
func runMCP(ctx context.Context, args []string, stderr io.Writer) error {
	storePath, err := parseMCPConfig(args, stderr)
	if err != nil {
		return err
	}

	return withStores(ctx, storePath, func(_ *source.Store, memories *memory.Store) error {
		server := mcp.NewServer(memory.NewRecaller(memories))
		return server.Run(ctx, &mcpsdk.StdioTransport{})
	})
}

// parseMCPConfig accepts "my mcp [serve] [--store <path>]" and resolves the
// store path, defaulting to the shared import/ingest store.
func parseMCPConfig(args []string, stderr io.Writer) (string, error) {
	flagArgs := args[1:]
	if len(flagArgs) > 0 && flagArgs[0] == "serve" {
		flagArgs = flagArgs[1:]
	}

	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	storePath := flags.String("store", "", "SQLite memory store path")
	if err := flags.Parse(flagArgs); err != nil {
		return "", err
	}
	if *storePath != "" {
		return *storePath, nil
	}
	return defaultStorePath()
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
