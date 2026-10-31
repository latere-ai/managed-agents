// The shared per-push gates live in a module of their own so the port keeps
// its promise of depending on nothing. A tool requirement in the root go.mod
// would put latere.ai/x/ci-gate in every consumer's module graph for a program
// none of them run, and would create a root go.sum, which is the thing the
// dependencies gate refuses.
//
// The module owns the gate requirement and CI configuration tests, keeping
// their dependencies outside the port's module graph.
module latere.ai/x/managed-agents/internal/gate

go 1.27.3

tool latere.ai/x/ci-gate/cmd/lateregate

require github.com/goccy/go-yaml v1.19.2

require (
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/tools v0.52.0 // indirect
	latere.ai/x/ci-gate v0.60.0 // indirect
)
