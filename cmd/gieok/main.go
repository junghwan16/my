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

	"github.com/junghwan16/gieok/internal/embed"
	"github.com/junghwan16/gieok/internal/mcp"
	memoriespkg "github.com/junghwan16/gieok/internal/memory"
	"github.com/junghwan16/gieok/internal/migrate"
	sourcespkg "github.com/junghwan16/gieok/internal/source"
	"github.com/junghwan16/gieok/internal/storage"
	"github.com/junghwan16/gieok/internal/tokenize"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, time.Now().UTC()); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, now time.Time) error {
	if len(args) < 1 {
		return errUsage
	}
	if isHelpArg(args[0]) {
		return writeHelp(stderr, usageTop)
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

const usageTop = `usage: gieok <memory|mcp>

memory  save session files, build Memory, and recall it
mcp     expose recall/status/get tools to MCP clients`

const usageMemory = `usage: gieok memory <import|ingest|recall>

import  save Codex/Claude Code session files as Source
ingest  turn saved Sources into Memory
recall  find useful Memory for a task or question`

// errUsage is the top-level usage error.
var errUsage = errors.New(usageTop)

// errMemoryUsage is the memory command usage error.
var errMemoryUsage = errors.New(usageMemory)

func isHelpArg(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

func writeHelp(w io.Writer, usage string) error {
	if _, err := fmt.Fprintln(w, usage); err != nil {
		return fmt.Errorf("write help: %w", err)
	}
	return flag.ErrHelp
}

func setUsage(flags *flag.FlagSet, usage string) {
	flags.Usage = func() {
		if _, err := fmt.Fprintln(flags.Output(), usage); err != nil {
			return
		}
		if _, err := fmt.Fprintln(flags.Output()); err != nil {
			return
		}
		flags.PrintDefaults()
	}
}

func runMemory(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, now time.Time) error {
	if len(args) < 2 {
		return errMemoryUsage
	}
	if isHelpArg(args[1]) {
		return writeHelp(stderr, usageMemory)
	}

	switch args[1] {
	case "import":
		return runMemoryImport(ctx, args, stdout, stderr, now)
	case "ingest":
		return runMemoryIngest(ctx, args, stdout, stderr, now)
	case "recall":
		return runMemoryRecall(ctx, args, stdout, stderr)
	default:
		return errMemoryUsage
	}
}

// withStores opens the SQLite database, brings its schema up to date through the
// migration ledger, and hands ready stores to fn, guaranteeing the database is
// closed afterwards.
func withStores(ctx context.Context, path string, fn func(*sourcespkg.Store, *memoriespkg.Store) error) (err error) {
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
	memories := memoriespkg.NewStore(db, tokenizer)
	attachEmbedder(ctx, memories)
	if err = memories.EnsureFTSIndexed(ctx); err != nil {
		return err
	}
	if err = memories.EnsureVectorsIndexed(ctx); err != nil {
		return err
	}
	return fn(sourcespkg.NewStore(db), memories)
}

// attachEmbedder enables semantic recall when local Ollama is
// reachable. It health-checks the default bge-m3 embedder and attaches it only
// if available; otherwise the store keeps a nil embedder and recall stays
// lexical-only, so the default build works fully offline with no Ollama.
func attachEmbedder(ctx context.Context, memories *memoriespkg.Store) {
	embedder := embed.NewOllama()
	if embedder.Available(ctx) {
		memories.WithEmbedder(embedder)
	}
}

func runMemoryImport(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, now time.Time) error {
	config, err := parseMemoryImportConfig(args, stderr)
	if err != nil {
		return err
	}

	return withStores(ctx, config.storePath, func(sources *sourcespkg.Store, _ *memoriespkg.Store) error {
		logger := slog.New(slog.NewTextHandler(stderr, nil))
		result, err := sourcespkg.NewImporter(sources, logger).Import(ctx, config.from, now)
		if err != nil {
			return err
		}

		if _, err := fmt.Fprintf(
			stdout,
			"saved %d source(s) from session files, skipped %d unsupported file(s)\n",
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

	return withStores(ctx, config.storePath, func(sources *sourcespkg.Store, memories *memoriespkg.Store) error {
		logger := slog.New(slog.NewTextHandler(stderr, nil))
		ingester := memoriespkg.NewIngester(sources, memories, config.agents, logger)
		result, err := ingester.Ingest(ctx, config.options, now)
		if err != nil {
			return err
		}

		if _, err := fmt.Fprintf(
			stdout,
			"built %d memories from %d source(s), failed %d agent run(s)\n",
			result.Memories,
			result.Sources,
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

	return withStores(ctx, config.storePath, func(_ *sourcespkg.Store, memories *memoriespkg.Store) error {
		recallResults, err := memoriespkg.NewRecaller(memories).Recall(ctx, config.task, config.scope, config.limit)
		if err != nil {
			return err
		}
		if config.json {
			return writeRecallJSON(stdout, recallResults)
		}
		return writeRecallText(stdout, recallResults)
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
		return memoryRecallConfig{}, errors.New("usage: gieok memory recall [task] [--scope <value>] [--all-scopes] [--limit <n>] [--json] [--store <sqlite-db>]")
	}

	flags := flag.NewFlagSet("memory recall", flag.ContinueOnError)
	flags.SetOutput(stderr)
	setUsage(flags, "usage: gieok memory recall <task> [--scope <workspace>|--all-scopes] [--limit <n>] [--json] [--store <sqlite-db>]")
	storePath := flags.String("store", "", "SQLite file that stores gieok sources and memories")
	task := flags.String("task", "", "Task or question to find useful memory for")
	scope := flags.String("scope", "", "Workspace path to recall within (default: current directory)")
	allScopes := flags.Bool("all-scopes", false, "Recall across every workspace scope")
	limit := flags.Int("limit", 0, "Maximum number of memories to show (0 = default)")
	asJSON := flags.Bool("json", false, "Print the recall result as JSON")

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
	return map[string]bool{"all-scopes": true, "json": true, "h": true, "help": true}
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

	return withStores(ctx, storePath, func(_ *sourcespkg.Store, memories *memoriespkg.Store) error {
		server := mcp.NewServer(memoriespkg.NewRecaller(memories))
		return server.Run(ctx, &mcpsdk.StdioTransport{})
	})
}

// parseMCPConfig accepts "gieok mcp [serve] [--store <path>]" and resolves the
// store path, defaulting to the shared import/ingest store.
func parseMCPConfig(args []string, stderr io.Writer) (string, error) {
	flagArgs := args[1:]
	if len(flagArgs) > 0 && isHelpArg(flagArgs[0]) {
		return "", writeHelp(stderr, "usage: gieok mcp [serve] [--store <sqlite-db>]")
	}
	if len(flagArgs) > 0 && flagArgs[0] == "serve" {
		flagArgs = flagArgs[1:]
	}

	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	setUsage(flags, "usage: gieok mcp [serve] [--store <sqlite-db>]")
	storePath := flags.String("store", "", "SQLite file that stores gieok sources and memories")
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
	agents    []memoriespkg.Agent
	options   memoriespkg.IngestOptions
}

func parseMemoryImportConfig(args []string, stderr io.Writer) (memoryImportConfig, error) {
	if len(args) < 2 || args[0] != "memory" || args[1] != "import" {
		return memoryImportConfig{}, errors.New("usage: gieok memory import --from <path> --store <sqlite-db>")
	}

	flags := flag.NewFlagSet("memory import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	setUsage(flags, "usage: gieok memory import --from <file-or-dir> [--store <sqlite-db>]")
	from := flags.String("from", "", "Codex or Claude Code session file or directory to save as Source")
	storePath := flags.String("store", "", "SQLite file that stores gieok sources and memories")
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
		return memoryIngestConfig{}, errors.New("usage: gieok memory ingest --agent <name=command[,arg...]> --store <sqlite-db>")
	}

	flags := flag.NewFlagSet("memory ingest", flag.ContinueOnError)
	flags.SetOutput(stderr)
	setUsage(flags, "usage: gieok memory ingest [--agent <name=command[,arg...]>] [--source-id <source-id>] [--limit <n>] [--store <sqlite-db>]")
	storePath := flags.String("store", "", "SQLite file that stores gieok sources and memories")
	limit := flags.Int("limit", 0, "Maximum number of sources to turn into memory")
	concurrency := flags.Int("concurrency", 0, "Maximum agents to run at once (0 = default)")
	skipExisting := flags.Bool("skip-existing", false, "Skip source-agent pairs that already have memory")
	var agentSpecs repeatedFlag
	var sourceIDs repeatedFlag
	flags.Var(&agentSpecs, "agent", "Agent command as name=command[,arg...], or a built-in agent name (claude, codex, pi)")
	flags.Var(&sourceIDs, "source-id", "Source ID to turn into memory; repeatable")
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
			agents:    memoriespkg.DefaultAgents(),
			options:   options,
		}, nil
	}

	agents := make([]memoriespkg.Agent, 0, len(agentSpecs))
	for _, spec := range agentSpecs {
		agent, err := memoriespkg.ResolveAgentSpec(spec)
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

func newIngestOptions(sourceIDs []string, limit int, concurrency int, skipExisting bool) memoriespkg.IngestOptions {
	options := memoriespkg.IngestOptions{
		Limit:        limit,
		Concurrency:  concurrency,
		SkipExisting: skipExisting,
	}
	if len(sourceIDs) == 0 {
		return options
	}

	options.SourceIDs = make([]sourcespkg.SourceID, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		options.SourceIDs = append(options.SourceIDs, sourcespkg.SourceID(id))
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
	return filepath.Join(home, ".local", "share", "gieok", "memory", "gieok.db"), nil
}
