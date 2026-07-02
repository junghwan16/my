package eval

import (
	"context"
	"testing"
)

// fakeRecaller returns preset results keyed by scenario task.
type fakeRecaller struct {
	byTask map[string][]Result
}

func (f fakeRecaller) Recall(_ context.Context, task, _ string, limit int) ([]Result, error) {
	results := f.byTask[task]
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// fakeEmbedder maps known text to a fixed vector so cosine is deterministic.
// Unknown text embeds orthogonally (never matches).
type fakeEmbedder struct {
	vecs map[string][]float32
}

func (f fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if v, ok := f.vecs[text]; ok {
		return v, nil
	}
	return []float32{0, 0, 1}, nil
}

func TestRunHitsBySemanticSimilarity(t *testing.T) {
	// Expected and result 1 point the same way (cosine 1); result 0 is orthogonal.
	rec := fakeRecaller{byTask: map[string][]Result{
		"t": {{ID: "m0", Text: "off topic"}, {ID: "m1", Text: "on topic"}},
	}}
	emb := fakeEmbedder{vecs: map[string][]float32{
		"the knowledge": {1, 0, 0},
		"on topic":      {1, 0, 0},
		"off topic":     {0, 1, 0},
	}}
	scenarios := []Scenario{{ID: "s", Task: "t", ExpectedKnowledge: "the knowledge"}}

	report, err := Run(context.Background(), rec, emb, scenarios, 5, 0.6, 0.7)
	if err != nil {
		t.Fatal(err)
	}
	if report.Hits != 1 || !report.Results[0].Hit {
		t.Fatalf("expected a hit, got %+v", report.Results)
	}
	if report.Results[0].HitRank != 2 {
		t.Fatalf("hit rank = %d, want 2 (the on-topic result is second)", report.Results[0].HitRank)
	}
	if report.Results[0].BestSim < 0.99 {
		t.Fatalf("best sim = %.3f, want ~1.0", report.Results[0].BestSim)
	}
}

func TestRunMissesWhenBelowThreshold(t *testing.T) {
	rec := fakeRecaller{byTask: map[string][]Result{"t": {{ID: "m0", Text: "off topic"}}}}
	emb := fakeEmbedder{vecs: map[string][]float32{
		"the knowledge": {1, 0, 0},
		"off topic":     {0, 1, 0}, // orthogonal -> cosine 0
	}}
	report, err := Run(context.Background(), rec, emb, []Scenario{{ID: "s", Task: "t", ExpectedKnowledge: "the knowledge"}}, 5, 0.6, 0.7)
	if err != nil {
		t.Fatal(err)
	}
	if report.Hits != 0 || report.Results[0].Hit {
		t.Fatalf("expected a miss, got %+v", report.Results[0])
	}
}

func TestRunHitsByAnchorWithoutEmbedder(t *testing.T) {
	rec := fakeRecaller{byTask: map[string][]Result{
		"t": {{ID: "m0", Text: "mentions the DDL migration approach here"}},
	}}
	// Nil embedder: anchor substring is the only signal.
	report, err := Run(context.Background(), rec, nil, []Scenario{
		{ID: "s", Task: "t", Anchors: []string{"DDL migration"}},
	}, 5, 0.6, 0.7)
	if err != nil {
		t.Fatal(err)
	}
	if report.Hits != 1 || report.Results[0].HitRank != 1 {
		t.Fatalf("expected anchor hit at rank 1, got %+v", report.Results[0])
	}
	if report.EmbedderUse {
		t.Fatal("EmbedderUse should be false when embedder is nil")
	}
}

func TestRunAggregatesHitRateAndPass(t *testing.T) {
	rec := fakeRecaller{byTask: map[string][]Result{
		"hit":  {{ID: "a", Text: "x"}},
		"miss": {{ID: "b", Text: "y"}},
	}}
	scenarios := []Scenario{
		{ID: "s1", Task: "hit", Anchors: []string{"x"}},
		{ID: "s2", Task: "miss", Anchors: []string{"zzz"}},
	}
	report, err := Run(context.Background(), rec, nil, scenarios, 5, 0.6, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if report.HitRate != 0.5 {
		t.Fatalf("hit rate = %.2f, want 0.5", report.HitRate)
	}
	if !report.Pass {
		t.Fatal("0.5 hit rate should pass a 0.5 threshold")
	}
}
