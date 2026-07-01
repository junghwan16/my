// Package eval measures whether Recall is useful: it replays past tasks as
// scenarios and checks whether the knowledge a scenario expects shows up in the
// top Recall results. It is the quantitative feedback loop for tuning Recall —
// ranking parameters, prompt changes, and data-model changes are judged by
// whether this hit rate goes up, not by eyeballing a few queries.
//
// A scenario "hits" when a top result is about the expected knowledge, judged by
// embedding cosine similarity (the same signal semantic Recall ranks on) and/or
// a distinctive anchor substring. The package depends only on small interfaces
// so it can run against the real store or an in-memory fake.
package eval

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Result is one Recall result the eval judges — only the fields judging needs.
type Result struct {
	ID   string
	Text string
}

// Recaller runs Recall for a task within a scope, returning ranked results. The
// production *memories.Recaller is adapted to this by the caller.
type Recaller interface {
	Recall(ctx context.Context, task, scope string, limit int) ([]Result, error)
}

// Embedder turns text into a vector for cosine judging. An eval can run without
// one (anchor-only judging) by passing nil.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// Scenario is one replay: a past task, the scope to recall within, and the
// knowledge a useful recall must surface in the top results.
type Scenario struct {
	ID string `json:"id"`
	// Task is the prompt that starts the replay.
	Task string `json:"task"`
	// Scope is the workspace the recall is restricted to.
	Scope string `json:"scope"`
	// ExpectedKnowledge is the knowledge (in prose) a top result should carry.
	// It is embedded and compared to each result by cosine similarity.
	ExpectedKnowledge string `json:"expected_knowledge"`
	// Anchors are distinctive substrings; a top result containing any of them
	// counts as a lexical hit even if the embedder is unavailable. Optional.
	Anchors []string `json:"anchors,omitempty"`
}

// ScenarioResult records how one scenario scored.
type ScenarioResult struct {
	ID       string  `json:"id"`
	Hit      bool    `json:"hit"`
	HitRank  int     `json:"hit_rank"` // 1-based rank of the first hitting result; 0 if none
	BestSim  float64 `json:"best_similarity"`
	Returned int     `json:"returned"`
}

// Report is the aggregate outcome of an eval run. HitRate (top-k recall) and MRR
// (mean reciprocal rank, rewarding higher-ranked hits) are the two dependent
// variables an experiment moves; holding scenarios and corpus fixed and varying
// one factor (ranker, min-sim, limit) isolates that factor's effect.
type Report struct {
	Variant     string           `json:"variant,omitempty"`
	Results     []ScenarioResult `json:"results"`
	Hits        int              `json:"hits"`
	Total       int              `json:"total"`
	HitRate     float64          `json:"hit_rate"`
	MRR         float64          `json:"mrr"`
	MinSim      float64          `json:"min_similarity"`
	Limit       int              `json:"limit"`
	PassRate    float64          `json:"pass_rate"`
	Pass        bool             `json:"pass"`
	EmbedderUse bool             `json:"embedder_used"`
}

// Run replays every scenario and returns the aggregate hit report. A scenario
// hits when, within the top `limit` results, some result either clears minSim
// against the expected knowledge (when an embedder is provided) or contains a
// scenario anchor substring. passRate is the fraction of scenarios that must hit
// for the run to Pass (e.g. 0.70).
func Run(ctx context.Context, r Recaller, e Embedder, scenarios []Scenario, limit int, minSim, passRate float64) (Report, error) {
	report := Report{
		Total:       len(scenarios),
		MinSim:      minSim,
		Limit:       limit,
		PassRate:    passRate,
		EmbedderUse: e != nil,
	}

	for _, scenario := range scenarios {
		sr, err := runScenario(ctx, r, e, scenario, limit, minSim)
		if err != nil {
			return Report{}, fmt.Errorf("scenario %s: %w", scenario.ID, err)
		}
		report.Results = append(report.Results, sr)
		if sr.Hit {
			report.Hits++
			report.MRR += 1.0 / float64(sr.HitRank)
		}
	}

	if report.Total > 0 {
		report.HitRate = float64(report.Hits) / float64(report.Total)
		report.MRR /= float64(report.Total)
	}
	report.Pass = report.HitRate >= passRate
	return report, nil
}

func runScenario(ctx context.Context, r Recaller, e Embedder, scenario Scenario, limit int, minSim float64) (ScenarioResult, error) {
	results, err := r.Recall(ctx, scenario.Task, scenario.Scope, limit)
	if err != nil {
		return ScenarioResult{}, err
	}
	sr := ScenarioResult{ID: scenario.ID, Returned: len(results)}

	var expected []float32
	if e != nil && strings.TrimSpace(scenario.ExpectedKnowledge) != "" {
		expected, err = e.Embed(ctx, scenario.ExpectedKnowledge)
		if err != nil {
			return ScenarioResult{}, fmt.Errorf("embed expected knowledge: %w", err)
		}
	}

	for rank, result := range results {
		if anchorMatch(result.Text, scenario.Anchors) {
			markHit(&sr, rank+1, 1.0)
			continue
		}
		if len(expected) == 0 {
			continue
		}
		vec, err := e.Embed(ctx, result.Text)
		if err != nil {
			return ScenarioResult{}, fmt.Errorf("embed result: %w", err)
		}
		sim := cosine(expected, vec)
		if sim > sr.BestSim {
			sr.BestSim = sim
		}
		if sim >= minSim {
			markHit(&sr, rank+1, sim)
		}
	}
	return sr, nil
}

// markHit records the earliest hitting rank and keeps the best similarity seen.
func markHit(sr *ScenarioResult, rank int, sim float64) {
	if sim > sr.BestSim {
		sr.BestSim = sim
	}
	if !sr.Hit || rank < sr.HitRank {
		sr.Hit = true
		sr.HitRank = rank
	}
}

func anchorMatch(text string, anchors []string) bool {
	lower := strings.ToLower(text)
	for _, anchor := range anchors {
		anchor = strings.TrimSpace(strings.ToLower(anchor))
		if anchor != "" && strings.Contains(lower, anchor) {
			return true
		}
	}
	return false
}

// cosine returns the cosine similarity of two equal-length vectors, or 0 when
// they are empty or mismatched — so an incomparable pair simply never hits.
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// FormatText renders a human-readable report: a per-scenario line (hit/miss,
// rank, best similarity) followed by the aggregate hit rate and pass verdict.
func FormatText(report Report) string {
	var b strings.Builder
	sorted := append([]ScenarioResult(nil), report.Results...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, sr := range sorted {
		status := "MISS"
		rank := "-"
		if sr.Hit {
			status = "HIT"
			rank = fmt.Sprintf("@%d", sr.HitRank)
		}
		fmt.Fprintf(&b, "  %-4s %-3s sim=%.3f  (%d returned)  %s\n",
			status, rank, sr.BestSim, sr.Returned, sr.ID)
	}
	verdict := "FAIL"
	if report.Pass {
		verdict = "PASS"
	}
	fmt.Fprintf(&b, "hit rate %d/%d = %.0f%%  MRR %.3f  (min-sim %.2f, top-%d, threshold %.0f%%) -> %s\n",
		report.Hits, report.Total, report.HitRate*100, report.MRR, report.MinSim, report.Limit, report.PassRate*100, verdict)
	return b.String()
}

// FormatComparison renders an ablation table: the same scenarios and corpus
// scored under several variants (e.g. hybrid vs lexical vs semantic), so the
// contribution of the varied factor is read off one column. Variants are shown
// in the given order.
func FormatComparison(reports []Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-12s  %-10s  %-7s  %s\n", "variant", "hit rate", "MRR", "verdict")
	fmt.Fprintf(&b, "%-12s  %-10s  %-7s  %s\n", "-------", "--------", "---", "-------")
	for _, report := range reports {
		verdict := "FAIL"
		if report.Pass {
			verdict = "PASS"
		}
		rate := fmt.Sprintf("%d/%d=%.0f%%", report.Hits, report.Total, report.HitRate*100)
		fmt.Fprintf(&b, "%-12s  %-10s  %.3f    %s\n", report.Variant, rate, report.MRR, verdict)
	}
	return b.String()
}
