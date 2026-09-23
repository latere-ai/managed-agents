# Contributing

For someone changing this repository. If you are building an application on the port, read [the README](README.md); if you are writing an adapter, read [docs/adapters.md](docs/adapters.md).

## What lives where

```
schema/managed.json      the contract. the source of truth for every wire type
types.gen.go             generated from it. never edited by hand
internal/gen/            the generator. its own Go module
enum.go                  State and Grade only — hand-written, and why below
runtime.go               the Runtime interface
methods.go               methods on the generated types
errors.go                the error classes and the typed errors
validate.go              ValidateSpec, ValidateCapabilities, ValidateObservation
secrets.go               request-scoped secret resolution
contracttest/            the conformance suite every adapter must pass
memory/                  the reference runtime
testdata/                golden wire values
specs/                   design records — the argument behind each decision
docs/                    user-facing documentation
```

## Three rules

**1. The schema is the source.** Every wire type is generated. To change one, edit `schema/managed.json` and run `go generate ./...`. A test fails if the committed `types.gen.go` does not match what the schema produces, so the two cannot drift apart quietly.

The exceptions are `State` and `Grade`. They are strings on the wire but *ordered* in Go — a lifecycle that only moves forwards, and a judgment that is either decided or not — and the hand-written marshaling is what keeps the wire form independent of the numbering that order needs. Everything else is a plain string type and is generated.

**2. The module depends on nothing.** No import outside the standard library, in the package or its tests. `TestModuleDependsOnNothing` enforces it. This is the port's one promise to a third party: adopting the contract costs them nothing in their module graph. That is also why the generator and the CI gates are each their own module — a tool requirement in the root `go.mod` would reach every consumer for a program none of them run. `make deps` checks both halves: that no root `go.sum` exists at all, and that the packages a consumer builds import nothing outside the standard library.

**3. Policy stays out.** The port states what an adapter must do. How long to wait between polls, how many failures to tolerate, when to give up, whether a failed evidence handoff blocks release — those are an application's choices. When you are unsure which side of the line something falls on, ask: *does this constrain an adapter, or a caller?*

## Adding a rule

A rule is worth adding when an adapter can break it while compiling and passing its own tests. Every rule needs three things:

1. **A check.** Usually a new case in `ValidateObservation`, returning an error that unwraps to `ErrContract`.
2. **A case in `contracttest`.** Adapter authors find rules through the suite, not through the documentation.
3. **A statement in prose.** The rules are listed in the README, and they are written without Go so a non-Go implementer can follow them.

A rule that is only in the Go code is invisible to the TypeScript adapter someone writes next year.

## Adding a capability

`Capabilities` grows when a backend genuinely cannot do something and no existing flag says so. Before adding one, check that an existing flag does not already cover it — `DurableEvents` and `Resumable` are near neighbors, and conflating them was the bug that produced the second one.

A new flag needs a `contracttest` case that holds a runtime to *both* declarations. A capability nothing tests is a comment.

## Testing

```
make test         # go vet and the suite, the fast signal
make test-race    # the port and the generator under the race detector
make cover        # the coverage gate, judged per package
make all          # every gate, which is exactly what CI runs
```

CI runs no command of its own. It probes this Makefile and runs the targets it
finds, so a gate that passes here passes there for the same reason.

Coverage is above 90% and `make cover` holds it there, per package rather than as a repository average, against the floor and the one exemption in `.lateregate.yaml`. The reference runtime and the conformance suite are not scaffolding — they are how the contract is checked, so they carry tests of their own.

`memory.Config` scripts the reference runtime: how long a session runs, what grade it reaches, which files it produces, which call fails. Use it rather than writing a new fake.

## Documentation

Each surface has one audience, and mixing them is the failure to avoid:

| | For | Voice |
|---|---|---|
| `README.md`, `docs/` | People building on the port | What it does for them, and how to use it |
| `docs/adapters.md` | People implementing the port | Precise about the surface and the obligations |
| `specs/` | Whoever changes this repository next | The argument, the alternatives, what was rejected |
| Code comments | Whoever reads the line | Why it is this way, not what it does |

Keep the argument in `specs/` and out of the README. Keep the usage in the README and out of `specs/`.

## Private module

This repository is private, so `go` needs to be told not to look for it on the public proxy:

```
export GOPRIVATE=latere.ai/x/*
git config --global url."https://$GITHUB_TOKEN@github.com/latere-ai/".insteadOf "https://github.com/latere-ai/"
```

Continuous integration needs the same, through a token with read access. Without it `go get` fails at the proxy with a message that does not name the cause.

## Commits

One logical change per commit, staged explicitly — never `git add -A`. The message says *why* the change was made; the diff already says what it did.

## Writing

Every sentence the port emits or carries is written for one reader, and the
register follows the reader:

- User, a person or a coding harness: the documentation under `docs/`, the
  text an adapter shows from a typed error. Short and plain: what happened
  and what to do next, naming a command or a page, never a package, a
  function, a table, or a Kubernetes object.
- Contributor, someone changing the port: specs, this file, package
  documentation, commit messages, source comments. Precise, in the project's
  own terms, with the reason a design is what it is.
- Developer, someone debugging a running system: the typed errors' `Error()`
  text, the conformance suite's failure output. Exact and complete: object,
  operation, observed value, expected value, and the underlying error.

An error has one code, one fixed user sentence in `message`, and one
developer detail in a separate field shown only on request. The canonical
statement, worked examples, and the review checklist are in the registers
document in pkg:
https://github.com/latere-ai/pkg/blob/main/docs/writing/registers.md
The rule applies to new text and to reviews; existing text is fixed as it is
touched.
