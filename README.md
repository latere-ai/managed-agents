# managed-agents

One interface for running a sandboxed, tool-using model session — whichever vendor actually runs it.

```go
spec := managed.SessionSpec{
    Model:        managed.ModelSelector{Preference: "best"},
    Instructions: "You are a careful research engineer.",
    Task:         "Run the analysis and write results.md to the output directory.",
    Sandbox: managed.SandboxSpec{
        Packages: []managed.Package{{Manager: "pip", Name: "numpy"}},
        Network:  managed.NetworkPolicy{Mode: managed.NetworkNone},
    },
}

ref, err := runtime.Start(ctx, spec)     // runtime is any adapter
status, err := runtime.Poll(ctx, ref)
events, err := runtime.Events(ctx, ref)  // read the evidence...
err = runtime.Release(ctx, ref)          // ...before you release it
```

Swap the adapter and nothing above changes.

## Why this exists

Vendors sell the same capability in shapes that agree on almost nothing. One hosts the session remotely and hands back an identifier you can poll from any process. One drives the model loop inside *your* process against a hosted sandbox. One is a library that runs entirely in memory, where a session ends when the process does.

Writing an application against all three usually means branching per vendor at every call site, or writing to the widest one and stubbing the rest. The stubs are the problem: a blocking in-process runner is easy to dress up as a pollable session, it passes every test, and it fails the first time a process restarts — as a lost run rather than an error.

This package fixes that in two ways. A runtime **declares** what it cannot do, and a request for more is refused before anything remote is created. And the rules that a type signature cannot state are written down and made executable, so an adapter can be *proved* to conform instead of assumed to.

## Install

```
go get latere.ai/x/managed-agents
```

The package name is `managed`:

```go
import managed "latere.ai/x/managed-agents"
```

It depends on nothing outside the Go standard library, and a test in the repository keeps it that way.

## What you get

| | |
|---|---|
| `managed` | The `Runtime` interface, the types that cross it, the errors, and the validation. |
| `managed/contracttest` | The black-box suite an adapter has to pass. One function call in your adapter's test. |
| `managed/memory` | A deterministic in-process runtime. Develop and test without a vendor account. |
| `latere.ai/x/managed-agents/latere` | The Latere platform: hosted agent sessions with a sandbox, on any model the platform offers. A module of its own. |
| `schema/managed.json` | The same contract as JSON Schema. The Go types are generated from it. |

## The rules

Most of the contract is not visible in the type signatures. These are the parts an adapter gets wrong silently:

1. **Usage is a cumulative total for the session, never a delta for one call.** No token bucket and no meter may decrease between two observations. An adapter that reports deltas gets its caller billed several times over.
2. **State moves forward only.** `pending → running → done | failed`, and a terminal observation stays terminal however often you poll it.
3. **The resolved model never changes** once the runtime has reported it.
4. **A graded session ends with a decided verdict. An ungraded one never carries a verdict at all.** Both halves are rules: a verdict nobody asked for is as wrong as a missing one.
5. **Evidence is readable until `Release`.** Read the transcript and fetch the artifacts *before* you release. `Release` is idempotent, and after it those calls report `ErrNotFound`.
6. **`Validate` is pure.** It reaches nothing remote and has no side effects, so you can call it on the way in.
7. **The locator inside a `SessionRef` is opaque.** Store it byte for byte, never parse it, and never expect a credential to be in it.

`managed.ValidateObservation` enforces rules 1 to 4 on any two observations. `contracttest` enforces all of them.

## Capabilities, not assumptions

Ask before you commit:

```go
capabilities, err := runtime.Capabilities(ctx)
if !capabilities.Resumable {
    // This runtime's session handles die with this process. If your service
    // needs to pick a session back up after a restart, refuse it here rather
    // than finding out during one.
}
```

`Capabilities` also states whether the runtime can grade, cancel, enumerate artifacts, store the transcript, give a session a file it can read and not change, or check a findings file every round, which package managers and network modes it supports, and how large an attachment may be. `ValidateCapabilities` refuses a specification asking for more than a runtime declares — before anything remote exists. There is no best-effort mode that quietly drops the part the runtime cannot do.

## What this is not

**It is not a poll loop.** There is no backoff here, no retry budget, and no rule about when a slow runtime should be given up on. Those are your operational choices, not claims about a vendor, so you write the loop. [docs/using-the-port.md](docs/using-the-port.md) shows one.

**The contract carries no vendor.** Each adapter is a Go module of its own, so importing the contract never brings in a vendor's code or dependencies.

**It does not price anything.** `Usage` reports quantities. Turning quantities into money needs a rate card, which changes on a different clock.

## Documentation

- [Using the port](docs/using-the-port.md) — driving a session from an application.
- [Writing an adapter](docs/adapters.md) — the interface, the rules, and the conformance suite.
- [The Latere runtime](docs/latere.md) — running sessions on the Latere platform.
- [The wire contract](docs/schema.md) — the JSON Schema, and generating types for another language.
- [Contributing](CONTRIBUTING.md) — changing this repository.

## License

Apache-2.0. See [LICENSE](LICENSE).
