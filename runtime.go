package managed

import "context"

// Capabilities is what a runtime can actually do, stated as semantic flags and
// limits rather than vendor product names. It exists so a specification is
// refused before anything remote is created: there is no best-effort mode in
// which an unsupported package manager, network mode, or oversized attachment
// is silently dropped.
type Capabilities struct {
	// OutputDir is the sandbox path a session is told to write its artifacts
	// to. A caller templates it into the instructions rather than hard-coding
	// one backend's filesystem layout.
	OutputDir string
	// PackageManagers are the managers this runtime can install from, keyed by
	// the name a Package carries.
	PackageManagers map[string]bool
	// NetworkModes are the egress policies this runtime can impose. A mode
	// absent here is refused, never downgraded to a stricter one that happens
	// to be supported.
	NetworkModes map[NetworkMode]bool
	// RepositoryModes are the repository access modes this runtime can arrange.
	RepositoryModes map[AccessMode]bool
	// Evaluation reports whether this runtime can grade a session against a
	// rubric. When false, a specification carrying an EvaluationSpec is
	// refused: grading is not something a caller can bolt on afterwards from a
	// transcript.
	Evaluation bool
	// DurableEvents reports whether the transcript is stored by the runtime
	// rather than buffered in this process. When false the evidence dies with
	// the process, so a caller that wants to keep it must read it before
	// letting the process end.
	DurableEvents bool
	// Artifacts reports whether the runtime can enumerate the files a session
	// produced. When false, ListArtifacts refuses rather than returning an
	// empty list, which would read as "the session produced nothing".
	Artifacts bool
	// Cancellation reports whether a running session can be stopped. When
	// false, Cancel refuses and a caller's only lever is to stop reading.
	Cancellation bool
	// Resumable reports whether a SessionRef survives the process that created
	// it. When true, a reference persisted now can be handed back to a fresh
	// process later and will still name a live session. When false the
	// reference is meaningful only inside the process that started the session:
	// an adapter over an in-process runtime declares false, and a caller that
	// must survive a restart refuses the runtime at admission rather than
	// discovering the loss when a restart happens.
	//
	// This is separate from DurableEvents on purpose. That flag is about the
	// evidence outliving the process; this one is about the handle doing so. A
	// runtime can lose one and keep the other.
	Resumable bool
	// MaxAttachmentBytes bounds one attachment's content. Zero means the
	// runtime declares no bound.
	MaxAttachmentBytes int64
	// MaxLocatorBytes bounds the locator this runtime writes into a reference.
	// Zero means DefaultMaxLocatorBytes.
	MaxLocatorBytes int
}

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
