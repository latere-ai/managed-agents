// Package memory is the reference runtime for the managed-agent port: a
// deterministic, in-process implementation that reaches no network.
//
// It exists to be the thing the conformance suite is validated against, and to
// give an application a runtime it can develop and test against without a
// vendor account. Config scripts its behaviour — how long a session runs, what
// verdict it reaches, which files it produces, and which call fails — so a
// caller's own error paths can be driven without a fake runtime wrapped around
// a real one.
package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	managed "latere.ai/x/managed-agents"
)

// Name identifies this runtime in a session reference.
const Name managed.RuntimeName = "memory"

// OutputDir is the workspace path a session writes its artifacts to. It is a
// filesystem path and deliberately not a RuntimeName: sharing a const block
// with Name left it untyped and one edit away from inheriting the wrong type.
const OutputDir = "/workspace/outputs/"

// FailurePoint names a runtime boundary that can be made to fail. Keying
// Config.Failures by one lets a test drive the error path of a single call
// without standing up a fake runtime around the real one.
type FailurePoint string

// The failure points, one per boundary a caller can reach. Each names the
// method it interrupts; the injected error surfaces from that method before it
// touches the store.
const (
	FailValidate FailurePoint = "validate"
	FailStart    FailurePoint = "start"
	FailPoll     FailurePoint = "poll"
	FailEvents   FailurePoint = "events"
	FailList     FailurePoint = "list_artifacts"
	FailFetch    FailurePoint = "fetch_artifact"
	FailCancel   FailurePoint = "cancel"
	FailRelease  FailurePoint = "release"
)

// Config is the scripted behaviour of one Runtime: which model it advertises,
// how many polls a session runs for, what verdict and artifacts it produces,
// and where it fails. NewWithStore fills in the zero values, so the zero
// Config is a usable happy path.
type Config struct {
	Model           managed.ModelRef
	PollsBeforeDone int
	Grade           managed.Grade
	Files           map[string][]byte
	Events          []managed.Event
	Failures        map[FailurePoint]error
	// NonResumable makes this runtime declare Capabilities.Resumable false and
	// behave accordingly: it keeps its sessions privately instead of in the
	// shared Store, so a Runtime rebuilt around that Store finds nothing. It is
	// how a test drives the in-process shape — an embeddable runtime whose
	// sessions cannot outlive the process that started them.
	NonResumable bool
	// NoArtifacts makes this runtime declare Capabilities.Artifacts false and
	// refuse both artifact calls. Returning an empty list instead would read as
	// "the session produced nothing", which is the confusion the flag exists to
	// prevent.
	NoArtifacts bool
	// NoCancellation makes this runtime declare Capabilities.Cancellation false
	// and refuse Cancel, rather than accepting it and carrying on.
	NoCancellation bool
	// NoEvaluation makes this runtime declare Capabilities.Evaluation false, so
	// a graded specification is refused by Validate. It needs no other change:
	// a session that cannot start cannot reach a verdict.
	NoEvaluation bool
}

// Store holds the sessions. It lives outside Runtime on purpose: that is what
// makes this runtime resumable: a Runtime rebuilt around the same Store still
// finds the sessions an earlier Runtime started, which is how a test models a
// process restart. Keeping the map on Runtime would have made every rebuild
// silently lose them — which is exactly what Config.NonResumable asks for.
type Store struct {
	mu       sync.Mutex
	next     uint64
	sessions map[string]*session
}

type session struct {
	spec      managed.SessionSpec
	ref       managed.SessionRef
	model     managed.ModelRef
	polls     int
	cancelled bool
	released  bool
}

// NewStore returns an empty Store, ready to be shared by every Runtime that
// should see the same sessions.
func NewStore() *Store { return &Store{sessions: make(map[string]*session)} }

// Runtime is the in-process managed-session runtime. It is safe for
// concurrent use: all session state is guarded by its Store's mutex, and the
// Config is read-only after construction.
type Runtime struct {
	store  *Store
	config Config
}

// New returns a Runtime on the default Config with a private Store, for the
// common case where one test owns the whole session lifecycle.
func New() *Runtime { return NewWithStore(NewStore(), Config{}) }

// NewWithStore returns a Runtime over an existing Store, which is how a test
// simulates a restart or injects failures. A nil store gets a fresh one.
// Unset Config fields take their defaults here rather than at use: one model,
// one poll to completion, and empty file and failure maps.
func NewWithStore(store *Store, config Config) *Runtime {
	if store == nil || config.NonResumable {
		// A non-resumable runtime never shares state with another Runtime,
		// which is what makes a rebuild lose the sessions rather than find
		// them. Honouring the caller's Store here would advertise durability
		// the capability says it does not have.
		store = NewStore()
	}
	if !config.Model.Valid() {
		config.Model = managed.ModelRef{Provider: "memory", ID: "memory-model"}
	}
	if config.PollsBeforeDone <= 0 {
		config.PollsBeforeDone = 1
	}
	if config.Files == nil {
		config.Files = make(map[string][]byte)
	}
	if config.Failures == nil {
		config.Failures = make(map[FailurePoint]error)
	}
	return &Runtime{store: store, config: config}
}

// Name is the runtime name stamped into every SessionRef this runtime issues
// and the one validateRef demands back.
func (r *Runtime) Name() managed.RuntimeName { return Name }

// Capabilities advertises every capability the contract defines, so a spec
// exercising any of them is accepted here and the path under test is the
// caller's rather than a capability rejection. The one exception is
// resumability, which Config.NonResumable turns off.
func (r *Runtime) Capabilities(ctx context.Context) (managed.Capabilities, error) {
	if err := contextError(ctx); err != nil {
		return managed.Capabilities{}, err
	}
	return r.capabilities(), nil
}

// capabilities is the context-free half of Capabilities. Validate needs the
// same answer and has no context to pass; borrowing a background one there
// would claim an I/O boundary this runtime does not have, and forced the error
// it cannot return to be discarded.
func (r *Runtime) capabilities() managed.Capabilities {
	return managed.Capabilities{
		OutputDir:          OutputDir,
		PackageManagers:    map[string]bool{"pip": true},
		NetworkModes:       map[managed.NetworkMode]bool{managed.NetworkNone: true, managed.NetworkOpen: true},
		RepositoryModes:    map[managed.AccessMode]bool{managed.AccessReadOnly: true, managed.AccessReadWrite: true},
		Evaluation:         !r.config.NoEvaluation,
		DurableEvents:      true,
		Artifacts:          !r.config.NoArtifacts,
		Cancellation:       !r.config.NoCancellation,
		Resumable:          !r.config.NonResumable,
		MaxAttachmentBytes: 64 << 20,
		MaxLocatorBytes:    managed.DefaultMaxLocatorBytes,
	}
}

// Models reports the single configured model under all three preferences, so
// any preference a spec asks for resolves.
func (r *Runtime) Models(ctx context.Context) ([]managed.Model, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	return []managed.Model{{Ref: r.config.Model, Preferences: []string{"best", "balanced", "fast"}}}, nil
}

// Validate checks the spec against the advertised capabilities and rejects an
// exact model that is not the configured one. It has no context to honour, so
// it reads the capabilities directly rather than through Capabilities.
func (r *Runtime) Validate(spec managed.SessionSpec) error {
	if err := r.failure(FailValidate); err != nil {
		return err
	}
	if err := managed.ValidateCapabilities(Name, r.capabilities(), spec); err != nil {
		return err
	}
	if spec.Model.Exact != nil && *spec.Model.Exact != r.config.Model {
		return &managed.UnsupportedError{Runtime: Name, Field: "model.exact", Capability: "model:" + spec.Model.Exact.ID}
	}
	return nil
}

// Start validates the spec before recording anything, so a rejected spec
// leaves no session behind. Session ids come from a counter on the Store and
// are therefore stable for a given sequence of Starts.
func (r *Runtime) Start(ctx context.Context, spec managed.SessionSpec) (managed.SessionRef, error) {
	if err := contextError(ctx); err != nil {
		return managed.SessionRef{}, err
	}
	if err := r.Validate(spec); err != nil {
		return managed.SessionRef{}, err
	}
	if err := r.failure(FailStart); err != nil {
		return managed.SessionRef{}, err
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	r.store.next++
	id := fmt.Sprintf("session-%d", r.store.next)
	locator, err := json.Marshal(struct {
		Store string `json:"store"`
	}{Store: "memory"})
	if err != nil {
		return managed.SessionRef{}, fmt.Errorf("encode locator: %w", err)
	}
	ref := managed.SessionRef{Runtime: Name, ID: id, Revision: 1, LocatorVersion: 1, Locator: locator}
	r.store.sessions[id] = &session{spec: spec, ref: ref, model: r.config.Model}
	return ref, nil
}

// Poll advances the session by one step and reports the state it reaches. A
// cancelled session fails immediately; otherwise the session runs until
// Config.PollsBeforeDone polls have been counted and then completes. A spec
// carrying an evaluation also gets a verdict at the configured grade, treating
// an unset grade as passed and anything other than passed as an evaluation
// failure. Timestamps are fixed and reported usage grows by one per poll, so
// the sequence is both reproducible and monotonic as the contract requires.
func (r *Runtime) Poll(ctx context.Context, ref managed.SessionRef) (managed.Status, error) {
	if err := r.before(ctx, FailPoll, ref); err != nil {
		return managed.Status{}, err
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	s, err := r.sessionLocked(ref)
	if err != nil {
		return managed.Status{}, err
	}
	s.polls++
	status := managed.Status{Ref: s.ref, Model: s.model, State: managed.StateRunning, StartedAt: time.Unix(1, 0), Usage: managed.Usage{Model: s.model, InputFresh: int64(s.polls)}}
	if s.cancelled {
		status.State = managed.StateFailed
		status.TerminalReason = managed.ReasonCancelled
		status.FinishedAt = time.Unix(2, 0)
		return status, nil
	}
	if s.polls < r.config.PollsBeforeDone {
		return status, nil
	}
	status.State = managed.StateDone
	status.TerminalReason = managed.ReasonSucceeded
	status.FinishedAt = time.Unix(2, 0)
	if s.spec.Evaluation != nil {
		grade := r.config.Grade
		if grade == managed.GradeUnknown {
			grade = managed.GradePassed
		}
		status.Verdict = &managed.Verdict{Grade: grade, Explanation: "recorded by memory runtime"}
		if grade != managed.GradePassed {
			status.State = managed.StateFailed
			status.TerminalReason = managed.ReasonEvaluationFailed
		}
	}
	return status, nil
}

// Events returns the configured transcript, copied, so a caller mutating the
// slice it gets back cannot rewrite what the next call reports.
func (r *Runtime) Events(ctx context.Context, ref managed.SessionRef) ([]managed.Event, error) {
	if err := r.before(ctx, FailEvents, ref); err != nil {
		return nil, err
	}
	return append([]managed.Event(nil), r.config.Events...), nil
}

// ListArtifacts reports the configured files sorted by path, which fixes the
// generated artifact ids: the same file keeps the same id across calls, and
// FetchArtifact can compare whole artifact values.
func (r *Runtime) ListArtifacts(ctx context.Context, ref managed.SessionRef) ([]managed.Artifact, error) {
	if err := r.before(ctx, FailList, ref); err != nil {
		return nil, err
	}
	if r.config.NoArtifacts {
		return nil, &managed.UnsupportedError{Runtime: Name, Field: "artifacts", Capability: "artifacts"}
	}
	paths := slices.Sorted(maps.Keys(r.config.Files))
	artifacts := make([]managed.Artifact, 0, len(paths))
	for index, filePath := range paths {
		artifacts = append(artifacts, managed.Artifact{ID: fmt.Sprintf("artifact-%d", index+1), Path: filePath})
	}
	return artifacts, nil
}

// FetchArtifact returns a copy of the file body, and only for an artifact
// equal in full to one ListArtifacts returned. Matching the whole value rather
// than the id or the path alone means a caller cannot fetch a path it never
// saw listed; a mismatch is a NotFoundError.
func (r *Runtime) FetchArtifact(ctx context.Context, ref managed.SessionRef, artifact managed.Artifact) ([]byte, error) {
	if err := r.before(ctx, FailFetch, ref); err != nil {
		return nil, err
	}
	artifacts, err := r.ListArtifacts(ctx, ref)
	if err != nil {
		return nil, err
	}
	if slices.Contains(artifacts, artifact) {
		return append([]byte(nil), r.config.Files[artifact.Path]...), nil
	}
	return nil, &managed.NotFoundError{Runtime: Name, ID: artifact.ID}
}

// Cancel marks the session cancelled. Nothing else changes until the next
// Poll, which is where the cancellation becomes a terminal state.
func (r *Runtime) Cancel(ctx context.Context, ref managed.SessionRef) error {
	if err := r.before(ctx, FailCancel, ref); err != nil {
		return err
	}
	if r.config.NoCancellation {
		return &managed.UnsupportedError{Runtime: Name, Field: "cancellation", Capability: "cancellation"}
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	s, err := r.sessionLocked(ref)
	if err != nil {
		return err
	}
	s.cancelled = true
	return nil
}

// Release retires the session and is idempotent: an unknown or already
// released session is not an error. That is why it repeats the checks by hand
// instead of going through before, which would turn the second call into a
// NotFoundError.
func (r *Runtime) Release(ctx context.Context, ref managed.SessionRef) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := validateRef(ref); err != nil {
		return err
	}
	if err := r.failure(FailRelease); err != nil {
		return err
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	s, ok := r.store.sessions[ref.ID]
	if !ok || s.released {
		return nil
	}
	s.released = true
	return nil
}

// Specs returns the specs of every live session in the Store, ordered by
// session id. It is outside the managed.Runtime contract and exists so a test
// can assert on what the caller actually submitted.
func (r *Runtime) Specs() []managed.SessionSpec {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	ids := slices.Sorted(maps.Keys(r.store.sessions))
	result := make([]managed.SessionSpec, 0, len(ids))
	for _, id := range ids {
		result = append(result, r.store.sessions[id].spec)
	}
	return result
}

func (r *Runtime) before(ctx context.Context, point FailurePoint, ref managed.SessionRef) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := validateRef(ref); err != nil {
		return err
	}
	if err := r.failure(point); err != nil {
		return err
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	_, err := r.sessionLocked(ref)
	return err
}

func (r *Runtime) sessionLocked(ref managed.SessionRef) (*session, error) {
	s, ok := r.store.sessions[ref.ID]
	if !ok || s.released {
		return nil, &managed.NotFoundError{Runtime: Name, ID: ref.ID}
	}
	if !s.ref.SameIdentity(ref) {
		return nil, &managed.RuntimeMismatchError{Expected: Name, Actual: ref.Runtime}
	}
	return s, nil
}

func (r *Runtime) failure(point FailurePoint) error { return r.config.Failures[point] }

func validateRef(ref managed.SessionRef) error {
	if ref.Runtime != Name {
		return &managed.RuntimeMismatchError{Expected: Name, Actual: ref.Runtime}
	}
	return ref.Validate(managed.DefaultMaxLocatorBytes)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("nil context")
	}
	return ctx.Err()
}

var _ managed.Runtime = (*Runtime)(nil)
