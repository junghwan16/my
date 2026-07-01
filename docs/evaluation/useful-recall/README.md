# Useful Recall Evaluation

This directory holds replay scenarios for proving whether gieok's Recall is useful.

## Claim

In a workspace with relevant past Source and Memory, `gieok memory recall` should return at least one actionable Expected Memory in the first five results for a new task.

## First Trial

- Canonical Scope: `/Users/jeff.cho/personal/gieok`
- Initial evaluated Scope: `/Users/jeff.cho/personal/my`
- Scenario count: 10-15 real past tasks
- Pass threshold: at least 70% of scenarios have an Expected Memory in the top five results
- Fail guard: any top-five Memory that would clearly send the next agent in a wrong direction is recorded as a dangerous false positive

The first evaluated Scope uses the legacy `/Users/jeff.cho/personal/my` path because the
current canonical Scope has no imported Source or linked Memory in the present store.
Scope continuity across workspace path renames is tracked separately in GitHub issue #21.

## Scenario Shape

Each Replay Scenario should describe:

- the task prompt that starts the replay
- the Scope used for Recall
- the Expected Memory or specific knowledge that should appear
- the expected behavior change that Memory should cause
- optional notes about dangerous false positives

Synthetic examples can be used for ranking implementation tests, but they do not count toward this product-validity trial.
