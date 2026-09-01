// The shared per-push gates live in a module of their own so the port keeps
// its promise of depending on nothing. A tool requirement in the root go.mod
// would put latere.ai/x/ci-gate in every consumer's module graph for a program
// none of them run, and would create a root go.sum, which is the thing the
// dependencies gate refuses.
//
// The module holds no packages. It exists to own one requirement and the
// go.sum that pins it, the same reason internal/gen is a module.
module latere.ai/x/managed-agents/internal/gate

go 1.27.0

tool latere.ai/x/ci-gate/cmd/lateregate

require (
	github.com/goccy/go-yaml v1.19.2 // indirect
	latere.ai/x/ci-gate v0.25.2 // indirect
)
