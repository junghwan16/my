package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/junghwan16/gieok/internal/embed"
	"github.com/junghwan16/gieok/internal/eval"
	memoriespkg "github.com/junghwan16/gieok/internal/memory"
	sourcespkg "github.com/junghwan16/gieok/internal/source"
)

// runMemoryEval measures Recall usefulness against replay scenarios. It is the
// experiment surface: it holds the corpus and scenarios fixed and, with
// --mode all, scores the same scenarios under each ranker (hybrid / lexical /
// semantic) so the ranker's contribution is read off one comparison table.
func runMemoryEval(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	config, err := parseMemoryEvalConfig(args, stderr)
	if err != nil {
		return err
	}

	scenarios, err := loadScenarios(config.scenariosPath)
	if err != nil {
		return err
	}
	if len(scenarios) == 0 {
		return fmt.Errorf("no scenarios found in %s", config.scenariosPath)
	}

	return withStores(ctx, config.storePath, func(_ *sourcespkg.Store, memories *memoriespkg.Store) error {
		recaller := memoriespkg.NewRecaller(memories)

		// The embedder judges semantic hits. Absent Ollama, judging degrades to
		// scenario anchors only, which the report flags via embedder_used.
		var judge eval.Embedder
		if embedder := embed.NewOllama(); embedder.Available(ctx) {
			judge = embedder
		}

		reports := make([]eval.Report, 0, len(config.variants()))
		for _, variant := range config.variants() {
			report, err := eval.Run(ctx, adaptRecaller(recaller, variant), judge,
				scenarios, config.limit, config.minSim, config.passRate)
			if err != nil {
				return err
			}
			report.Variant = variant
			reports = append(reports, report)
		}

		if config.json {
			return json.NewEncoder(stdout).Encode(reports)
		}
		if len(reports) == 1 {
			_, err := fmt.Fprint(stdout, eval.FormatText(reports[0]))
			return err
		}
		_, err := fmt.Fprint(stdout, eval.FormatComparison(reports))
		return err
	})
}

// adaptRecaller wraps one ranker of the shared Recaller as an eval.Recaller, so
// the eval package stays ranker-agnostic and the CLI chooses the independent
// variable (hybrid Recall, lexical Search, or semantic SearchSemantic).
func adaptRecaller(recaller *memoriespkg.Recaller, mode string) eval.Recaller {
	switch mode {
	case "lexical":
		return recallerFunc(func(ctx context.Context, task, scope string, limit int) ([]eval.Result, error) {
			memories, err := recaller.Search(ctx, task, scope, limit)
			return memoriesToResults(memories), err
		})
	case "semantic":
		return recallerFunc(func(ctx context.Context, task, scope string, limit int) ([]eval.Result, error) {
			memories, err := recaller.SearchSemantic(ctx, task, scope, limit)
			return memoriesToResults(memories), err
		})
	default: // hybrid
		return recallerFunc(func(ctx context.Context, task, scope string, limit int) ([]eval.Result, error) {
			results, err := recaller.Recall(ctx, task, scope, limit)
			return recallResultsToResults(results), err
		})
	}
}

type recallerFunc func(ctx context.Context, task, scope string, limit int) ([]eval.Result, error)

func (f recallerFunc) Recall(ctx context.Context, task, scope string, limit int) ([]eval.Result, error) {
	return f(ctx, task, scope, limit)
}

func memoriesToResults(memories []memoriespkg.Memory) []eval.Result {
	results := make([]eval.Result, 0, len(memories))
	for _, memory := range memories {
		results = append(results, eval.Result{ID: string(memory.ID), Text: memory.Text})
	}
	return results
}

func recallResultsToResults(recalled []memoriespkg.RecallResult) []eval.Result {
	results := make([]eval.Result, 0, len(recalled))
	for _, result := range recalled {
		results = append(results, eval.Result{ID: string(result.MemoryID), Text: result.Text})
	}
	return results
}

type memoryEvalConfig struct {
	storePath     string
	scenariosPath string
	mode          string
	limit         int
	minSim        float64
	passRate      float64
	json          bool
}

// variants is the ordered list of rankers to score: all three for an ablation,
// otherwise the single selected mode.
func (c memoryEvalConfig) variants() []string {
	if c.mode == "all" {
		return []string{"hybrid", "lexical", "semantic"}
	}
	return []string{c.mode}
}

const defaultScenariosPath = "docs/evaluation/useful-recall/scenarios.json"

func parseMemoryEvalConfig(args []string, stderr io.Writer) (memoryEvalConfig, error) {
	if len(args) < 2 || args[0] != "memory" || args[1] != "eval" {
		return memoryEvalConfig{}, errors.New("usage: gieok memory eval [--scenarios <path>] [--mode hybrid|lexical|semantic|all] [--limit <n>] [--min-sim <f>] [--pass <f>] [--json] [--store <sqlite-db>]")
	}

	flags := flag.NewFlagSet("memory eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	setUsage(flags, "usage: gieok memory eval [--scenarios <path>] [--mode hybrid|lexical|semantic|all] [--limit <n>] [--min-sim <f>] [--pass <f>] [--json] [--store <sqlite-db>]")
	storePath := flags.String("store", "", "SQLite memory store path")
	scenariosPath := flags.String("scenarios", defaultScenariosPath, "Replay scenarios JSON file")
	mode := flags.String("mode", "hybrid", "Ranker to score: hybrid, lexical, semantic, or all")
	limit := flags.Int("limit", 5, "Top-k results considered per scenario")
	// 0.45 is calibrated for bge-m3, whose cosine is compressed: measured real
	// hits score ~0.55 while a 0.60 floor missed every true hit (see
	// runs/2026-07-02-partial-baseline-ablation.yaml). It mirrors the store's own
	// 0.40 recall floor rather than a textbook 0.6.
	minSim := flags.Float64("min-sim", 0.45, "Cosine floor for an embedding hit (bge-m3 compressed; ~0.45)")
	passRate := flags.Float64("pass", 0.7, "Hit-rate fraction required to pass")
	asJSON := flags.Bool("json", false, "Emit the report(s) as JSON")
	if err := flags.Parse(args[2:]); err != nil {
		return memoryEvalConfig{}, err
	}

	switch *mode {
	case "hybrid", "lexical", "semantic", "all":
	default:
		return memoryEvalConfig{}, fmt.Errorf("invalid --mode %q (want hybrid, lexical, semantic, or all)", *mode)
	}
	if *limit <= 0 {
		return memoryEvalConfig{}, errors.New("--limit must be positive")
	}

	if *storePath == "" {
		defaultPath, err := defaultStorePath()
		if err != nil {
			return memoryEvalConfig{}, err
		}
		*storePath = defaultPath
	}

	return memoryEvalConfig{
		storePath:     *storePath,
		scenariosPath: *scenariosPath,
		mode:          *mode,
		limit:         *limit,
		minSim:        *minSim,
		passRate:      *passRate,
		json:          *asJSON,
	}, nil
}

// loadScenarios reads a JSON scenarios file of the form {"scenarios": [...]}.
func loadScenarios(path string) ([]eval.Scenario, error) {
	//nolint:gosec // The scenarios path is provided by the local CLI user.
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scenarios file: %w", err)
	}
	var file struct {
		Scenarios []eval.Scenario `json:"scenarios"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse scenarios file %s: %w", path, err)
	}
	return file.Scenarios, nil
}
