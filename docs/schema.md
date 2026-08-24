# The wire contract

`schema/managed.json` is the contract. The Go types in `types.gen.go` are generated from it, and a port to another language generates from the same document — so the two cannot drift.

If you only write Go, you never need this file. It is here for a non-Go implementation, and for anyone who wants to validate what crosses a boundary.

## What is in it

One JSON Schema (draft 2020-12) document with 26 definitions under `$defs`: the session specification and everything it contains, the session reference, an observation, a transcript event, an artifact, and the capability declaration.

```
SessionSpec ─┬─ ModelSelector ── ModelRef
             ├─ SandboxSpec ─┬─ Package
             │               └─ NetworkPolicy ── NetworkMode
             ├─ Resource ─┬─ AttachmentResource
             │            └─ RepositoryResource ── AccessMode
             └─ EvaluationSpec

Status ──┬─ SessionRef ── RuntimeName
         ├─ ModelRef
         ├─ Usage ── Meter
         └─ Verdict

Event ── Role, EventKind, Usage        Artifact        Capabilities
```

## Generating types for another language

Point any JSON Schema code generator at the document. Six `x-` keywords carry what a general-purpose generator cannot infer; a generator that ignores them still produces correct shapes, just less idiomatic names.

| Keyword | Where | Says |
|---|---|---|
| `x-type-order` | root | The order to emit the definitions in. A JSON object has no order, and alphabetical output scatters related types. |
| `x-field-order` | object | The same for that object's properties. |
| `x-go-name` | property | The Go spelling of an initialism: `id` is `ID`, `sha256` is `SHA256`, `url` is `URL`. |
| `x-go-type` | property | A verbatim Go type, for raw payloads (`json.RawMessage`), timestamps (`time.Time`), and the two enums whose Go form is hand-written. |
| `x-go-key-type` | map | The key type of a map, so a meter map is `map[Meter]int64`. Paired with `propertyNames` so the constraint is language-neutral too. |
| `x-go-open` | string type | The named values are documentation, not validation. |

**Closed and open enums are spelled differently, on purpose.** A closed set states its values under `oneOf`, each entry carrying `const`, a `title` naming it, and a `description` — so a validator in any language rejects an unknown value, and a generator still gets names and docs. An open set (`EventKind`, `Meter`) states the same list under `x-go-consts` and *no* `oneOf`, so an unknown value passes validation unchanged. That difference is load-bearing: a transcript is evidence, and coercing a runtime's own event kind into a neighbouring one would falsify it.

**Optionality.** A property absent from `required` is optional. Whether that becomes a pointer is derived rather than declared: an optional *object* needs one to distinguish absent from zero; everything else takes its zero value.

## What the schema cannot say

The schema describes shapes. Most of the contract is rules about *sequences* of values, and no JSON Schema expresses any of them: usage never decreases, state moves forward only, a terminal observation stays terminal, the resolved model never changes, a verdict appears exactly when grading was requested, and evidence stays readable until release.

Generating types from this document gives you the shapes and none of the rules. Read [the rules in the README](../README.md#the-rules) alongside it. In Go, `managed.ValidateObservation` and the `contracttest` suite are their executable form; in another language you have to write that yourself.

## Changing the schema

The schema is the source. Edit it, then regenerate:

```
go generate ./...
```

A test fails if `types.gen.go` does not match what the schema produces, so a schema edit that was never regenerated cannot be committed quietly. The golden values in `testdata/` are validated against the schema *and* round-tripped through the Go types, which is what catches a `required` list or a field name that describes something the port does not actually emit.
