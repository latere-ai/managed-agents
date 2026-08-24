package managed

import (
	"context"
	"maps"
)

type secretContextKey struct{}

// WithSecrets attaches request-scoped secret values to a session start. Specs
// carry only stable references; values are never serialized into a spec or ref.
func WithSecrets(ctx context.Context, values map[string]string) context.Context {
	copyValues := make(map[string]string, len(values))
	maps.Copy(copyValues, values)
	return context.WithValue(ctx, secretContextKey{}, copyValues)
}

// Secret resolves one request-scoped reference without exposing the complete
// secret set to an adapter.
func Secret(ctx context.Context, ref string) (string, bool) {
	values, _ := ctx.Value(secretContextKey{}).(map[string]string)
	value, ok := values[ref]
	return value, ok
}
