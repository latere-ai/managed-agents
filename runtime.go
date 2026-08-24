package managed

import "context"

// Runtime is one backend able to execute a session. The interface is
// deliberately small enough to fake, because every adapter is held to the same
// conformance suite.
//
// Validate is pure and mandatory, and Start repeats it defensively before any
// remote write; Start returns a reference only once that reference alone is
// enough to reach the session again. Evidence stays readable after a session
// goes terminal or is cancelled, until Release completes — which is why capture
// must precede release.
//
// Every method that can block takes a context and must honour its
// cancellation, so a session can be abandoned from outside.
type Runtime interface {
	// Name is the canonical name this runtime answers to, and the name every
	// reference it issues carries.
	Name() RuntimeName

	// Capabilities reports what this runtime can do. It creates nothing and
	// mutates nothing, so a caller may ask before deciding to start at all.
	Capabilities(context.Context) (Capabilities, error)

	// Models lists the models this runtime offers and the preference tiers each
	// one can serve.
	Models(context.Context) ([]Model, error)

	// Validate reports whether this runtime would accept the specification. It
	// is pure: it consults nothing remote and has no side effects.
	Validate(SessionSpec) error

	// Start creates the session and returns as soon as it exists, without
	// waiting for it to finish.
	Start(context.Context, SessionSpec) (SessionRef, error)

	// Poll reports the current state of a session without blocking. Successive
	// observations must satisfy ValidateObservation.
	Poll(context.Context, SessionRef) (Status, error)

	// Events returns the session's transcript as the runtime normalised it, in
	// the order the runtime reports. It is readable until Release.
	Events(context.Context, SessionRef) ([]Event, error)

	// ListArtifacts lists the files the session produced. It is readable until
	// Release.
	ListArtifacts(context.Context, SessionRef) ([]Artifact, error)

	// FetchArtifact reads one artifact's bytes. It is readable until Release.
	FetchArtifact(context.Context, SessionRef, Artifact) ([]byte, error)

	// Cancel stops a live session. Cancelling a session that has already
	// finished is success, and the evidence stays readable afterwards.
	Cancel(context.Context, SessionRef) error

	// Release frees the session's remote resources. It must be safe to call
	// more than once. After it returns, Events, ListArtifacts, and
	// FetchArtifact report ErrNotFound.
	Release(context.Context, SessionRef) error
}
