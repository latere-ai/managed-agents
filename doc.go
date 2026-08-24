// Package managed is the provider-neutral contract for running one graded,
// sandboxed, tool-using model session.
//
// A runtime is any backend able to execute such a session: a vendor that hosts
// the session remotely and hands back an identifier, a library that drives the
// model loop inside this process, or anything between. An application states
// what it wants as a [SessionSpec], hands it to a [Runtime], and works with
// [Status], [Event], and [Artifact] — never with a vendor's own vocabulary.
//
// # What this package does not do
//
// It does not poll. There is no loop here, no backoff, no retry budget, and no
// rule about when to give up on a slow runtime. Those are operational choices
// an application makes, not claims about what a backend does, and an
// application drives [Runtime] in whatever way suits it.
//
// It also contains no adapter. The only implementation shipped here is a
// deterministic in-memory runtime for tests, in the memory subpackage.
//
// # The rules
//
// Most of the contract is not in the type signatures. It is a set of rules
// about sequences of observations, and an adapter that ignores them compiles
// perfectly and loses money or evidence at runtime:
//
//   - [Usage] is the cumulative total for the session, never a per-call delta.
//     No bucket and no meter may decrease between two observations.
//   - [State] moves forward only, and a terminal observation stays terminal.
//   - The resolved model never changes once reported.
//   - A graded session ends with a decided [Verdict]; an ungraded one never
//     carries one at all.
//   - Evidence stays readable until [Runtime.Release], which is idempotent.
//     Capture before you release.
//   - [SessionSpec.Validate] is pure. It consults nothing remote.
//   - A [SessionRef] locator is opaque: stored byte for byte, never parsed,
//     never carrying a credential.
//
// [ValidateObservation] enforces the first four on any two observations, and
// the contracttest subpackage is the black-box suite that holds an adapter to
// all of them.
//
// # Declared capabilities
//
// Backends differ in what they can actually do, and the differences are not
// cosmetic: some cannot grade, some cannot enumerate the files a session
// produced, and some cannot hand back a reference that outlives the process
// that created it. [Capabilities] states each of those as a flag or a limit,
// and [ValidateCapabilities] refuses a specification asking for more than a
// runtime declares — before anything remote is created. There is no
// best-effort mode in which an unsupported request is quietly dropped.
package managed

//go:generate go -C internal/gen run . -schema ../../schema/managed.json -out ../../types.gen.go
