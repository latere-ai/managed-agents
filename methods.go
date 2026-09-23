package managed

import (
	"path"
	"strings"
)

// CanonicalRuntimeName folds operator-supplied text into the single spelling a
// RuntimeName may take. Routes, catalog entries, and references are compared
// as plain strings, so "Anthropic" and " anthropic " must not become two
// runtimes; validation rejects any name that is not already its own canonical
// form.
func CanonicalRuntimeName(name string) RuntimeName {
	return RuntimeName(strings.ToLower(strings.TrimSpace(name)))
}

// Valid reports whether the reference names a model concretely enough to price
// and to record. Both halves are required: an id without a provider cannot be
// matched to a rate card, because two vendors may publish the same id.
func (m ModelRef) Valid() bool {
	return strings.TrimSpace(m.Provider) != "" && strings.TrimSpace(m.ID) != ""
}

// SameIdentity reports whether two references name the same session, ignoring
// the checkpoint. It is the guard that stops a status observation or a
// persisted update from being applied to a different session: only Revision and
// Locator are allowed to move between observations.
func (r SessionRef) SameIdentity(other SessionRef) bool {
	return r.Runtime == other.Runtime && r.ID == other.ID
}

// Passed reports whether the evaluator decided for the session. Every other
// grade, undecided ones included, is not a pass, so the question is safe to ask
// before the evaluation has finished.
func (v Verdict) Passed() bool { return v.Grade == GradePassed }

// TotalTokens sums every token bucket. Because the buckets are mutually
// exclusive it double-counts nothing, but it is a figure for display and sanity
// checks rather than for billing: the buckets carry different rates.
func (u Usage) TotalTokens() int64 {
	return u.InputFresh + u.InputCached + u.CacheWrite + u.CacheWrite5m + u.CacheWrite1h + u.Output
}

// CacheWritten is every cache-write bucket together, whatever cache lifetime
// the provider charged for. A caller asking how much was written to cache
// should not have to know how many time-to-live variants exist.
func (u Usage) CacheWritten() int64 { return u.CacheWrite + u.CacheWrite5m + u.CacheWrite1h }

// Add accumulates another usage into u, bucket by bucket and meter by meter, so
// a session's stages can be totaled. A zero Usage is a usable accumulator: the
// maps are allocated only when the other side has something to put in them.
func (u *Usage) Add(other Usage) {
	u.InputFresh += other.InputFresh
	u.InputCached += other.InputCached
	u.CacheWrite += other.CacheWrite
	u.CacheWrite5m += other.CacheWrite5m
	u.CacheWrite1h += other.CacheWrite1h
	u.Output += other.Output
	if len(other.Extra) > 0 && u.Extra == nil {
		u.Extra = make(map[Meter]int64, len(other.Extra))
	}
	for meter, value := range other.Extra {
		u.Extra[meter] += value
	}
	if len(other.Details) > 0 && u.Details == nil {
		u.Details = make(map[string]int64, len(other.Details))
	}
	for name, value := range other.Details {
		u.Details[name] += value
	}
}

// MonotonicFrom reports whether u is a legal successor to previous: no token
// bucket and no extra meter may decrease. Usage is defined as the session
// total, so a decrease means the runtime sent a per-call delta or reset its
// counters, and that is a contract violation rather than something to absorb
// quietly.
func (u Usage) MonotonicFrom(previous Usage) bool {
	if u.InputFresh < previous.InputFresh || u.InputCached < previous.InputCached ||
		u.CacheWrite < previous.CacheWrite || u.CacheWrite5m < previous.CacheWrite5m ||
		u.CacheWrite1h < previous.CacheWrite1h || u.Output < previous.Output {
		return false
	}
	for meter, value := range previous.Extra {
		if u.Extra[meter] < value {
			return false
		}
	}
	return true
}

// Passed reports whether the session finished with a passing verdict. A session
// with no verdict was never graded, which is not the same as success, so it
// does not pass.
func (s Status) Passed() bool { return s.Verdict != nil && s.Verdict.Passed() }

// IsCompiled reports whether the artifact is build residue rather than a
// result. Such files are machine-specific: they describe the sandbox that
// produced them and reproduce nothing, so they are the first thing to drop when
// a session's output is kept. The path is normalized before the test, so a
// backslash-separated path cannot smuggle a __pycache__ entry past it.
func (a Artifact) IsCompiled() bool {
	clean := path.Clean(strings.ReplaceAll(a.Path, "\\", "/"))
	return strings.HasSuffix(clean, ".pyc") || strings.HasSuffix(clean, ".pyo") || strings.Contains(clean, "__pycache__/")
}
