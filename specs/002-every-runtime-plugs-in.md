---
title: Every runtime plugs in here
status: designed
---

# 002 — Every runtime plugs in here

**Status:** designed. The decision below (keep this port and grow it, rather than start again) is taken. Criteria 1 to 4 hold: the contract additions, the zero revision budget, their conformance cases and the adapter guide. The Latere adapter is built. ReplicHAI builds against this module with its copy of the contract deleted, on a branch that is not merged: its CI cannot fetch a private module, so criterion 6 holds once this repository is public. The Anthropic adapter is not moved yet; it still sits in ReplicHAI over that application's older provider protocol, and moving it means retiring that protocol from ReplicHAI's tests as well.

**Scope:** make this module the one contract every managed-session runtime implements, for ReplicHAI first: bring back the three things ReplicHAI added to its own copy after this port was extracted, correct what the port says about grading, ship the Anthropic and Latere adapters here as their own modules, and leave a designed slot for an OpenAI adapter. ReplicHAI then imports this module and deletes its internal copy.

**Depends on:** [001](001-the-managed-agent-port.md).

## Problem

This port was extracted from ReplicHAI's `internal/managed` package. Nothing imports it. ReplicHAI kept working on its own copy, so the two have drifted, and the question is whether the port is still the right base or whether it is cheaper to start again from what ReplicHAI has now.

ReplicHAI now needs a second hosted runtime (the Latere platform: Topos sessions, Cella sandboxes, Lux models), and its owner wants any runtime to be easy to add. If every adapter is written against ReplicHAI's internal copy, the contract can only be reused by copying it again.

## What drifted, measured

The two copies were compared type by type on 2026-10-15 (this module at `eaf45d7`, ReplicHAI at `06b12bb`).

Identical, field for field and tag for tag: `SessionSpec`, `ModelSelector`, `ModelRef`, `Model`, `SandboxSpec`, `Package`, `NetworkPolicy`, `AttachmentResource`, `RepositoryResource`, `SessionRef`, `Verdict`, `Usage`, `Status`, `Event`, `Artifact`, the error classes, the `Runtime` interface, and the validation of a specification, a reference and a network policy.

Only in this port:

- `Capabilities.Resumable`, and the conformance case that holds a runtime to both answers.
- JSON tags on `Capabilities`, so a capability declaration can cross a process.
- `ValidateObservation` as public API, with the `Grading` tri-state.
- A stricter conformance suite: every evidence read after `Release` must be `ErrNotFound`, and resumability is tested through a restart seam.
- The JSON Schema and the generator that produces the Go types from it.
- No dependency outside the standard library.

Only in ReplicHAI, each added for its repeat-run study:

- `InputResource` and `Capabilities.InputDir`: a file a session reads and may not change, such as the one copy of a paper every run of a study is given, placed outside the workspace and mounted read-only.
- `EvaluationSpec.Findings` and `Capabilities.FindingsRounds`: the workspace file that holds a run's findings as data, which a runtime that grades on its own side checks after every worker stage.
- `EvaluationSpec.MaxRevisions` may be zero, which grades the first attempt and never revises it. The port still requires a positive budget.
- `Controller`: the wait policy, retries, start and checkpoint hooks, evidence retention before release, and cancellation. The port left this out on purpose (001, "What the port is, and is not").

## Decision: keep the port and grow it

The port is the base. It is not redone.

1. **Every type a runtime touches already matches.** The drift is three additions on ReplicHAI's side and five on this side, and none of them conflicts with another. Starting again would rewrite the same structs and throw away the schema pipeline and the stricter suite.
2. **The port's additions are the ones a second runtime needs.** `Resumable` is the flag that tells a durable hosted runtime from one whose sessions die with the process, and the stricter suite is what an adapter written by someone else is held to.
3. **ReplicHAI's additions are product facts, not ReplicHAI policy.** A read-only input, a findings file checked each round, and a zero revision budget are things a caller asks a runtime for, so they belong in the contract, with capabilities that let a runtime refuse them.
4. **The controller stays with the application.** Poll timing, retry budgets and what happens when evidence cannot be kept are an application's choices. ReplicHAI keeps its controller; it moves next to its runtime catalog.
5. **It is the easier choice to reverse.** ReplicHAI pins a commit of this module. Its internal copy stays in its git history, and going back is one revert, where a rewrite would leave two contracts to reconcile.

Rejected: **redo the port from ReplicHAI's copy.** It would give the same types under a new history, lose `Resumable`, the public observation check and the schema, and leave no reason to believe the second extraction would not drift the same way.

Rejected: **keep the contract inside ReplicHAI and add adapters there.** It is the cheapest today, and it is exactly what the owner asked to stop: an adapter written against an internal package can only be reused by copying it.

## What changes in the port

**The three additions come back, through the schema.** `InputResource`, `Capabilities.InputDir`, `EvaluationSpec.Findings` and `Capabilities.FindingsRounds` are added to `schema/managed.json` and regenerated. `MaxRevisions` accepts zero. `ValidateCapabilities` refuses an input when `InputDir` is empty and a findings file when `FindingsRounds` is false. Each gets a conformance case that holds a runtime to both declarations, as the contributing rules require, and the memory runtime implements both.

**The grading rule is corrected.** `docs/adapters.md` says a runtime whose vendor cannot grade must refuse an evaluation and must not "grade it yourself from the transcript". That was written for a vendor with no grader at all. It is wrong for a runtime that runs the grader as a separate session: ReplicHAI's local runtime does, and the Latere runtime will, because Topos has no grader of its own. The rule becomes: a runtime may declare `Evaluation` when the grader runs in a context separate from the worker's, reads the work and the rubric rather than the worker's account of it, and ends in the same terminal states as a vendor grader. Grading inside the worker's own session, or from what the worker says it did, is still refused.

**Adapters live here, each as its own module.** The port promises that adopting it costs nothing in a consumer's module graph, and the repository already keeps its gate tool in a nested module for that reason. Each adapter gets the same treatment:

```
latere.ai/x/managed-agents              the contract, contracttest, memory (standard library only)
latere.ai/x/managed-agents/anthropic    Anthropic Managed Agents (needs the Anthropic SDK)
latere.ai/x/managed-agents/latere       the Latere platform: Topos sessions, Lux models
latere.ai/x/managed-agents/openai       designed, not built
```

A consumer that uses only the contract never sees the Anthropic SDK. 001 said adapters live in their own repositories; that was a guess about who would write them. The two that exist are written by the same people as the contract, and keeping them beside it means a contract change and the adapters it breaks are one commit.

**Adding a runtime is one package and one test.** `docs/adapters.md` gains a short checklist: create the module, implement `Runtime`, declare capabilities honestly, call `contracttest.Suite{New: ..., Restart: ...}.Run(t)`, and document the configuration it needs. The application side (a catalog entry, configuration, and a label) is the application's own documentation.

## The OpenAI slot

The `openai` module is designed and not built. OpenAI offers two shapes a runtime could sit on: Codex cloud tasks, a hosted session over a repository, and background Responses with a hosted shell container. Which one ReplicHAI uses is decided when the work starts, against the API as it is then. Either way the adapter declares what that API enforces: if it cannot run with an open network, it refuses `NetworkOpen` rather than guessing an allowlist, and if a task's handle does not survive a process, it declares `Resumable` false.

## Acceptance criteria

1. The schema carries `InputResource`, `InputDir`, `Findings` and `FindingsRounds`, and the generated types match it byte for byte.
2. `MaxRevisions: 0` validates; a negative budget does not.
3. The conformance suite holds a runtime to both declarations of `InputDir` and `FindingsRounds`, and the memory runtime passes it.
4. `docs/adapters.md` states the corrected grading rule and the checklist for adding a runtime.
5. The `anthropic` and `latere` modules each pass `contracttest`, and the root module still has no dependency outside the standard library.
6. ReplicHAI builds against this module with its internal copy of the contract deleted.
