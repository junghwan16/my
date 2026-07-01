# Recall Tuning — Experimental Design

Recall is tuned as a controlled experiment, not by eyeballing a few queries. This
file fixes the protocol so results are comparable across changes.

## Objective and metrics

**Claim under test:** in a workspace with relevant past Source and Memory,
`gieok memory recall` returns an actionable Expected Memory in the top five for a
new task.

Dependent variables, reported by `gieok memory eval`:

- **hit@k** — fraction of scenarios with an Expected Memory in the top `k` (primary; k=5).
- **MRR** — mean reciprocal rank of the first hit (rewards ranking the hit higher).
- **dangerous false positives** — top-k Memory that would send the next agent the
  wrong way (recorded by hand in the run notes; not yet automated).

Pass bar: hit@5 ≥ 70%.

## Independent variables (one at a time)

Hold the corpus and scenario set fixed; vary exactly one factor per experiment:

1. **Ranker** — hybrid vs lexical vs semantic (`--mode all`). Isolates what each
   ranker contributes.
2. **Semantic floor** — `defaultMinSimilarity` in `internal/memory/store.go`
   (rebuild required). Recall-first vs precision-first.
3. **Top-k / RRF k / overfetch** — ranking-shape parameters.
4. **Corpus quality** — focused memories (post issue #24 prompt) vs whole-session
   dumps. Compared across two re-ingested store snapshots.

## Protocol

1. Freeze a store snapshot and `scenarios.json` (the control).
2. Run `gieok memory eval --mode all --store <snapshot>` to get a baseline table.
3. Change one factor, rebuild if needed, re-run against the **same** snapshot.
4. Record a run file under `runs/<date>-<factor>.yaml`: git head, store, the
   factor changed, the metric table, and any dangerous false positives. Runs are
   the lab notebook — every claim traces to one.

## Threats to validity (read before trusting a number)

- **Judge circularity.** The eval judges a hit by embedding-cosine between the
  Expected Knowledge and each result — the *same* signal semantic Recall ranks on.
  This biases hit@k toward the semantic (and hybrid) ranker. Mitigations: each
  scenario also carries ranker-independent **anchors** (distinctive substrings)
  that score a hit without the embedder; when comparing rankers, weight anchor
  hits. A future independent judge (LLM or human-labeled Expected Memory ids)
  would remove the bias.
- **Underpowered set.** Three scenarios is directional, not significant. Grow to
  10–15 real past tasks before treating a delta as real. Split into a tuning set
  and a held-out set so parameter choices are not overfit.
- **Scope continuity.** Expected Memory currently lives under the legacy
  `/Users/jeff.cho/personal/my` Scope, not the canonical `/personal/gieok`
  (issue #21). Scenarios point at `/my`; a fair trial needs the Scope where the
  data actually is.
- **Corpus confound.** The store is being re-ingested with the improved prompt.
  Compare rankers only within one corpus snapshot; never across a changing store.

## Running it

```sh
# ablation across rankers on the default scenarios
gieok memory eval --mode all

# single ranker, stricter floor, as JSON for a run file
gieok memory eval --mode hybrid --min-sim 0.65 --json
```
