# Writing an adapter

For someone putting a vendor behind this port. An adapter implements `managed.Runtime` and passes `contracttest`. Nothing else is required, and nothing else is accepted.

An adapter can live in this repository, as its own Go module beside the contract so that a program importing only the contract never downloads a vendor's SDK, or in a repository of its own. Either way the obligations below are the same. The adapters here:

| Module | Runtime |
|---|---|
| `latere.ai/x/managed-agents/latere` | The Latere platform. See [the Latere runtime](latere.md). |
| `latere.ai/x/managed-agents/openai` | Planned, not built. |

An adapter here builds against the contract in the same commit (a `replace` to the parent directory in its `go.mod`), and its module directory goes in `ADAPTERS` in the Makefile, so every gate that tests the contract tests it too.

## Adding a runtime

1. Create a module for it: a directory here with its own `go.mod`, or a repository of its own.
2. Implement `managed.Runtime`. Start from the `memory` runtime: it is short, and every method in it states the rule it follows.
3. Declare in `Capabilities` what the vendor actually does, and call `managed.ValidateCapabilities` from `Validate`, so a request for more is refused before anything is created.
4. Run the suite with a `Restart`, so the resumability you declare is tested: `contracttest.Suite{New: ..., Restart: ...}.Run(t)`.
5. Add a test that runs one real session, skipped when the vendor credential is absent.
6. Document the configuration the adapter needs: the credential, the base address, and anything a caller must set before `Start`.

The application side, a name in its own catalog and the configuration it reads, belongs to the application's documentation, not this one.

## The interface

```go
type Runtime interface {
    Name() RuntimeName
    Capabilities(context.Context) (Capabilities, error)
    Models(context.Context) ([]Model, error)
    Validate(SessionSpec) error
    Start(context.Context, SessionSpec) (SessionRef, error)
    Poll(context.Context, SessionRef) (Status, error)
    Events(context.Context, SessionRef) ([]Event, error)
    ListArtifacts(context.Context, SessionRef) ([]Artifact, error)
    FetchArtifact(context.Context, SessionRef, Artifact) ([]byte, error)
    Cancel(context.Context, SessionRef) error
    Release(context.Context, SessionRef) error
}
```

Every blocking method takes a context and must honor cancellation.

## Declare honestly, then refuse

`Capabilities` is the whole reason the port can cover backends that differ this much. Declare what your vendor actually does:

```go
func (r *Runtime) Capabilities(context.Context) (managed.Capabilities, error) {
    return managed.Capabilities{
        OutputDir:          "/workspace/outputs/",
        PackageManagers:    map[string]bool{"pip": true, "npm": true},
        NetworkModes:       map[managed.NetworkMode]bool{managed.NetworkNone: true, managed.NetworkAllowlist: true},
        RepositoryModes:    map[managed.AccessMode]bool{managed.AccessReadOnly: true},
        Evaluation:         false,  // this vendor does not grade
        DurableEvents:      true,
        Artifacts:          true,
        Cancellation:       true,
        Resumable:          true,   // sessions outlive this process
        InputDir:           "/mnt/inputs/", // read-only files go here
        FindingsRounds:     false,  // grading happens on the vendor's side
        MaxAttachmentBytes: 32 << 20,
    }, nil
}
```

Then let the port do the refusing:

```go
func (r *Runtime) Validate(spec managed.SessionSpec) error {
    if err := managed.ValidateCapabilities(r.Name(), r.capabilities(), spec); err != nil {
        return err
    }
    // ...anything only you can check, such as an exact model you do not serve
    return nil
}
```

`ValidateCapabilities` runs `spec.Validate()` first, so one call covers both. `Validate` must be **pure**: no network, no side effects. `Start` calls it again before any remote write, because a caller that skipped it must not be able to create anything.

Two mistakes to avoid:

- **Do not downgrade.** If a caller asks for `NetworkOpen` and you support only `NetworkAllowlist`, refuse. Silently tightening produces a session that fails deep inside the run for reasons the caller cannot see.
- **Do not drop.** An unsupported package manager is `ErrUnsupported`, not a session that runs without the dependency.

### The capabilities people get wrong

| Flag | False means | Then |
|---|---|---|
| `Evaluation` | Nothing can grade the work against a rubric. | Refuse a spec carrying `Evaluation`. |
| `InputDir` (empty) | You cannot give a session a file it can read and not change. | Refuse a spec carrying an input. Do not copy it into the workspace, where the agent could edit it or hand it back as its own output. |
| `FindingsRounds` | You cannot check the findings file after every round. | Refuse a spec naming `Findings`. A vendor that grades on its own side cannot reach inside that loop. |
| `Artifacts` | You cannot enumerate what the session produced. | `ListArtifacts` refuses. An empty list would read as "it produced nothing". |
| `Cancellation` | A started session cannot be stopped. | `Cancel` refuses. Do not return nil and keep running. |
| `Resumable` | A `SessionRef` dies with this process. | A reference from a previous process must give `ErrNotFound`, never an invented status. |

`Evaluation` does not require the vendor to have a grader of its own. You may declare it when you run the grader yourself, as long as the grader works in a context separate from the worker's, reads the work and the rubric rather than the worker's account of the work, and ends the session in the same terminal states a vendor grader would. A second session, or a subagent with its own context and read-only tools, qualifies. Grading inside the worker's own conversation, or from what the worker says it did, does not: that is the worker marking its own work.

`Resumable` is the one worth dwelling on. If your vendor is an in-process library or an SDK that drives the loop on the caller's goroutine, you have no session to recover. Declaring `true` and answering a restarted `Poll` from a fresh in-memory map is the failure this flag exists to prevent: it works in every test and loses a live run on the first deploy.

It is not the same as `DurableEvents`. That one is about the *evidence* outliving the process; `Resumable` is about the *handle* doing so. A backend can lose one and keep the other.

## The reference

`Start` returns a `SessionRef`, and it must be enough to reach the session again on its own.

```go
locator, _ := json.Marshal(struct {
    Region  string `json:"region"`
    Shard   string `json:"shard"`
}{Region: "eu-central", Shard: shard})

ref := managed.SessionRef{
    Runtime:        r.Name(),
    ID:             vendorSessionID,
    Revision:       1,
    LocatorVersion: 1,
    Locator:        locator,
}
```

- `Runtime` and `ID` are fixed for the session's life.
- `Revision` starts at 1 and only ever increases. Raise it whenever the locator changes; the caller persists the new reference before polling you again, so a raised revision is a checkpoint you can rely on.
- `Locator` is yours. The caller stores and returns it byte for byte and never parses it. It **must not** contain a credential or user content, and it is size-bounded (`MaxLocatorBytes`, defaulting to 64 KiB). `SessionRef.Validate` rejects a locator with a secret-looking key, so a token in there fails at the boundary rather than in a log.

Never interpret another adapter's reference. Check the name first and return `ErrRuntimeMismatch`:

```go
func (r *Runtime) check(ref managed.SessionRef) error {
    if ref.Runtime != r.Name() {
        return &managed.RuntimeMismatchError{Expected: r.Name(), Actual: ref.Runtime}
    }
    return ref.Validate(managed.DefaultMaxLocatorBytes)
}
```

## Observations

`Poll` returns one `Status`, and successive ones must satisfy `managed.ValidateObservation`. In practice:

- **`Usage` is the session total.** If your vendor reports per-call deltas, accumulate them yourself. A total that decreases is a contract violation, and the caller has no way to recover the real number.
- **Keep the buckets apart.** Fresh input, cached input, and each cache-write lifetime are priced differently. When the vendor reports a cache write without saying which lifetime it bought, put it in `CacheWrite` rather than guessing: "we do not know which" has to stay visible.
- **Non-token meters go in `Extra`,** keyed by a `Meter` you name. Decomposition that is *already counted* in a bucket — reasoning tokens inside `Output` — goes in `Details`, which pricing never iterates.
- **Report the resolved model.** `Status.Model` must be concrete, from the first observation. `ModelSelector.Preference` is a request; what ran is what the caller records and prices.
- **State never goes backwards,** and a terminal observation stays terminal. Poll a finished session ten times and get the same answer ten times.
- **Verdicts follow the request.** A graded session must reach a decided grade. An ungraded one must carry no verdict at all.

## Evidence and release

`Events`, `ListArtifacts`, and `FetchArtifact` stay readable after the session goes terminal *and* after it is cancelled, until `Release` completes. That is what lets a caller capture the run before letting it go.

`Release` must be idempotent. Releasing an unknown or already-released session is success, not an error. Afterwards the three read calls report `ErrNotFound`.

`Cancel` must also be idempotent, and cancelling an already-finished session is success.

Keep the runtime's own payload in `Event.Raw`. The transcript is evidence, and normalizing it down to this package's vocabulary and throwing the rest away destroys the part a reader may need most.

## Errors

Return the port's classes so callers can match on them:

```go
&managed.UnsupportedError{Runtime: r.Name(), Field: "sandbox.network.mode", Capability: "network:open"}
&managed.NotFoundError{Runtime: r.Name(), ID: ref.ID}
&managed.RuntimeMismatchError{Expected: r.Name(), Actual: ref.Runtime}
&managed.TemporaryError{Err: err, After: retryAfter}   // rate limits, 5xx, transport blips
```

`Capability` is a semantic identifier — `network:open`, `package_manager:pip` — not a vendor product name, so the refusal reads the same whichever adapter raised it.

You do not have to adopt `TemporaryError`. `managed.IsTemporary` matches structurally on any error with a `Temporary() bool` method, so a transport's own retryable error can pass straight through.

## Prove it

```go
func TestConformance(t *testing.T) {
    contracttest.Run(t, func() managed.Runtime { return myadapter.New(config) })
}
```

`Run` covers the ungraded and graded lifecycles, that cancelling keeps the evidence readable, that release is idempotent and closes the reads, and that another runtime's reference is refused by all five methods that take one. Where a capability has two answers, the suite checks the one you declared: an input runs and never comes back as an artifact when you declare `InputDir`, and is refused when you do not; a findings file is graded when you declare `FindingsRounds`, and refused when you do not. Cases that need a capability you do not declare are skipped.

To cover resumability, supply a `Restart` — a runtime backed by the same state as a new process would see — and, for a runtime that grades, a `Rejecting` runtime whose grader rejects the work:

```go
contracttest.Suite{
    New:       func() managed.Runtime { return myadapter.New(config) },
    Restart:   func(managed.Runtime) managed.Runtime { return myadapter.New(config) },
    Rejecting: func() managed.Runtime { return myadapter.New(rejectingConfig) },
}.Run(t)
```

A rejected session must end failed, as an evaluation failure, carrying the rejecting verdict.

The suite then holds you to what you declared: `Resumable: true` must find the session again, and `Resumable: false` must report `ErrNotFound`. **An adapter that declares `Resumable: true` and supplies no `Restart` has not been tested on the claim it makes.**

If your adapter needs a vendor credential, skip the test when it is absent rather than failing it.
