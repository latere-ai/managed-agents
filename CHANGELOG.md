# Changelog

Every tag has a section here, and the section is the body of the GitHub
release. A tag without one is refused at the pre-push and fails the release
workflow. Write under `Unreleased` as work lands; `lateregate release vX.Y.Z`
turns that into the tag's section, commits, tags and pushes.

A section says what changed for whoever uses the release, not what was
committed: the commit log already holds that.

## Unreleased

### Added

- An input resource: a file a session reads and may not change, placed under the runtime's declared `InputDir`, outside the workspace. A runtime that declares no input directory refuses it.
- `EvaluationSpec.Findings` and `Capabilities.FindingsRounds`: the workspace file holding a run's findings as data, checked after every round by a runtime that declares it, and refused by one that does not.
- `MeterChargedCost`, for the cost a runtime's own platform has already charged, and `DetailVendorCost`, for a vendor's own figure reported beside the caller's price.
- Conformance cases that hold a runtime to both answers for inputs and findings rounds, and the memory runtime's switches for each.
- A checklist for adding a runtime, in the adapter guide.
- `latere.ai/x/managed-agents/latere`, a module of its own: sessions on the Latere platform, paid from the wallet of the person who starts them, on any model the platform offers, graded by a grader subagent. See `docs/latere.md`.
- `contracttest.Suite.Rejecting`, a runtime whose grader rejects the work, for the case that a rejected session ends failed with the rejecting verdict.

### Changed

- **Breaking:** the Latere runtime uses the agents API's subagent names, with no fallback to the old ones. It finds the grader by its `subagent.started` event and the grader's messages by their `subagent` field, and a graded worker's agent sets its limits on subagents as `spec.subagentLimits`. It asks the worker to start the grader with the `agent` tool and its `subagent` input, where it said `spawn` and `agent`, and recognizes only that request, so a graded session that was already running when you upgraded is asked for its grade once more. The platform now refuses a manifest that says `spec.threads`, so a build of this module from before this change can no longer start a session there. Agent names come from a digest of the spec, so every agent gets a new name and the first session after upgrading applies a new agent.
- The module is licensed under Apache-2.0, and every source file says so.
- The memory runtime declares allowlist egress, as it declares every other capability the contract defines.
- `EvaluationSpec.MaxRevisions` may be zero, which grades the first attempt and never revises it. A negative budget is still invalid.
- A path carrying a NUL byte is refused wherever the specification takes a relative path.
- The adapter guide's grading rule: a runtime may declare `Evaluation` when it runs the grader itself in a separate context that reads the work and the rubric, not only when the vendor grades.
