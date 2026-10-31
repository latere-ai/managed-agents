// The generator is a module of its own so the port it generates keeps its
// promise of depending on nothing. A tool requirement in the root go.mod would
// reach every consumer's module graph for a program none of them run.
module latere.ai/x/managed-agents/internal/gen

go 1.27.0

require github.com/google/jsonschema-go v0.4.3
