// Code generated from managed.json. DO NOT EDIT.

package managed

import (
	"encoding/json"
	"time"
)

// RuntimeName identifies one runtime adapter — the thing that executes a
// session, which is not the same as the vendor that bills for the model it
// uses. Every durable reference carries one, so the name must be canonical for
// a persisted session to route back to the adapter that started it.
type RuntimeName string

// Meter names a billable quantity that is not a token bucket, such as a
// container second or a hosted tool call. The set is open because each runtime
// emits its own, but a meter must be priced before a session using it can
// settle.
type Meter string

// NetworkMode is the outbound network policy a sandbox runs under. The empty
// value is not a mode: validation reads an omitted mode as none, so a
// specification that forgets to say gets the closed sandbox rather than the
// open one.
type NetworkMode string

const (
	// NetworkNone closes the sandbox off. It may carry neither domains nor
	// secrets.
	NetworkNone NetworkMode = "none"

	// NetworkAllowlist permits the named domains and nothing else. It is the only
	// mode that may carry secrets, and each one is bound to a domain the allowlist
	// names.
	NetworkAllowlist NetworkMode = "allowlist"

	// NetworkOpen lifts every restriction. It may carry neither domains nor
	// host-bound secrets, because there is no host binding left to enforce.
	NetworkOpen NetworkMode = "open"
)

// AccessMode is how a session may use a mounted repository. Runtimes advertise
// the modes they honor in Capabilities, so a specification asking for more than
// the runtime allows is refused before the session starts.
type AccessMode string

const (
	// AccessReadOnly states that the session must not write to the repository. The
	// enforcement is the credential's own scope; the mode tells the runtime what
	// to arrange.
	AccessReadOnly AccessMode = "ro"

	// AccessReadWrite obliges the specification to name a credential reference,
	// since a mount the session can push to needs one.
	AccessReadWrite AccessMode = "rw"
)

// ResourceKind says which payload of a Resource is the live one. Exactly one
// payload may be set and it must be the one the kind names; validation rejects
// the mismatch rather than guessing.
type ResourceKind string

const (
	// ResourceAttachment carries file content inline with a SHA-256 the validator
	// recomputes.
	ResourceAttachment ResourceKind = "attachment"

	// ResourceRepository names an HTTPS repository to place at a destination
	// inside the workspace.
	ResourceRepository ResourceKind = "repository"
)

// TerminalReason says why a session stopped, and is carried only on a terminal
// Status. A failed state alone cannot tell a run the runtime broke on from one
// that was cancelled or timed out, and operators chase those in different
// places.
type TerminalReason string

const (
	// ReasonSucceeded is the work finishing on its own terms.
	ReasonSucceeded TerminalReason = "succeeded"

	// ReasonEvaluationFailed is the work finishing and the evaluator deciding
	// against it.
	ReasonEvaluationFailed TerminalReason = "evaluation_failed"

	// ReasonCancelled is a session stopped from outside.
	ReasonCancelled TerminalReason = "cancelled"

	// ReasonTimedOut is a session that stopped answering rather than one that
	// failed. The distinction is the point: a fault the runtime owned up to and a
	// runtime that went quiet are chased in different places.
	ReasonTimedOut TerminalReason = "timed_out"

	// ReasonRuntimeFailed is a fault the runtime reported.
	ReasonRuntimeFailed TerminalReason = "runtime_failed"

	// ReasonPolicyRefused is a session a runtime declined on policy grounds rather
	// than ran, without the work ever starting.
	ReasonPolicyRefused TerminalReason = "policy_refused"
)

// Role is who a transcript event is attributed to. The set is closed: an
// adapter maps every runtime-native speaker onto one of these.
type Role string

const (
	// RoleAgent is the model.
	RoleAgent Role = "agent"

	// RoleUser is the caller the model answers.
	RoleUser Role = "user"

	// RoleSystem is the harness around them both.
	RoleSystem Role = "system"
)

// EventKind is what a transcript event is. Unlike Role the set is deliberately
// open: the kinds below are named so callers compare against a constant rather
// than a bare string, while a runtime-native kind passes through unchanged
// rather than being dropped or coerced into a neighbor.
type EventKind string

const (
	// KindMessage is text from one of the roles.
	KindMessage EventKind = "message"

	// KindThinking is reasoning the model exposed.
	KindThinking EventKind = "thinking"

	// KindToolUse is the model calling a tool.
	KindToolUse EventKind = "tool_use"

	// KindToolResult is what the tool answered.
	KindToolResult EventKind = "tool_result"
)

// ModelRef is the exact, provider-qualified model a session ran on, and the key
// a rate card is resolved against. Provider is the inference and cost source
// rather than the runtime, so a session may run on one runtime and bill against
// another vendor's model. BillingVariant records a price-affecting applied
// class such as a service tier, and stays empty where the provider has one
// price.
type ModelRef struct {
	Provider       string `json:"provider"`
	ID             string `json:"id"`
	BillingVariant string `json:"billing_variant,omitempty"`
}

// ModelSelector is how a specification asks for a model. Exactly one form is
// set: a preference tier the runtime resolves for itself (best, balanced,
// fast), or one exact reference the caller insists on. Either way the runtime
// reports the model it actually resolved in Status, so a record of what ran
// never depends on the tier being re-resolved later.
type ModelSelector struct {
	Preference string    `json:"preference,omitempty"`
	Exact      *ModelRef `json:"exact,omitempty"`
}

// Model is one model a runtime offers, with the preference tiers it can serve.
// Tier resolution matches a requested preference against Preferences, so one
// reference may answer to several tiers where a runtime offers only one model.
type Model struct {
	Ref         ModelRef `json:"ref"`
	Preferences []string `json:"preferences,omitempty"`
}

// Package is one dependency installed into the sandbox before the session runs.
// Manager is drawn from the set an adapter declares it supports: a manager the
// adapter cannot install fails validation rather than being skipped so the
// session runs without its dependency. An empty Version leaves the choice to
// the manager.
type Package struct {
	Manager string `json:"manager"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// NetworkPolicy is the sandbox's egress rule. Mode governs the other two
// fields: none rejects both, allowlist requires at least one domain, and open
// refuses host-bound secrets. SecretRefs maps an environment variable name to a
// "domain:name" reference whose domain must itself be allowlisted; a
// specification carries references only, never secret values.
type NetworkPolicy struct {
	Mode           NetworkMode       `json:"mode"`
	AllowedDomains []string          `json:"allowed_domains,omitempty"`
	SecretRefs     map[string]string `json:"secret_refs,omitempty"`
}

// SandboxSpec is the machine the work happens on, stated explicitly so an
// adapter can refuse what it cannot provide before anything remote is created.
// Bootstrap entries are argv arrays rather than shell fragments, so nothing in
// a specification is handed to a shell to interpret.
type SandboxSpec struct {
	Packages  []Package     `json:"packages,omitempty"`
	Bootstrap [][]string    `json:"bootstrap,omitempty"`
	Network   NetworkPolicy `json:"network"`
}

// AttachmentResource is one file placed into the sandbox outside any mounted
// repository — an input the session must read but must not commit. SHA256 is
// verified against Content during validation, so a corrupted or substituted
// payload fails the specification instead of the run.
//
// Keeping attachments outside every repository destination is structural rather
// than stylistic: it is what makes "this data is not ours to redistribute"
// enforceable by the filesystem layout, not by the model's good behavior.
type AttachmentResource struct {
	Path      string `json:"path"`
	MediaType string `json:"media_type,omitempty"`
	Content   []byte `json:"content"`
	SHA256    string `json:"sha256"`
}

// RepositoryResource is a version-control repository the session works in. A
// runtime may mount it, clone it, or stage it some other way, as long as the
// resulting filesystem and write authority match Access. CredentialRef names a
// credential and never carries one; read-write access requires it, because an
// unauthenticated write mount would fail only once the session was already
// running.
//
// AccessReadOnly states intent. The enforcement is the credential's own scope,
// so a read-only destination must be given a read-only credential: the field
// tells the runtime what to arrange, it does not by itself prevent a push.
type RepositoryResource struct {
	URL           string     `json:"url"`
	Revision      string     `json:"revision,omitempty"`
	Destination   string     `json:"destination"`
	Access        AccessMode `json:"access"`
	CredentialRef string     `json:"credential_ref,omitempty"`
}

// Resource is the tagged union of things a session can be given. Kind selects
// which payload is set, and exactly one must be. An unknown kind fails
// validation rather than being ignored, so a session never runs quietly missing
// an input the caller asked for.
type Resource struct {
	Kind       ResourceKind        `json:"kind"`
	Attachment *AttachmentResource `json:"attachment,omitempty"`
	Repository *RepositoryResource `json:"repository,omitempty"`
}

// EvaluationSpec makes a session graded. An absent EvaluationSpec on
// SessionSpec is the only way to say "ungraded", which is why a present one
// must carry a non-empty rubric and a positive revision budget: a zero value
// must not be readable as grading against nothing.
type EvaluationSpec struct {
	Rubric       string `json:"rubric"`
	MaxRevisions int    `json:"max_revisions"`
}

// SessionSpec is the complete intent for one session, independent of the
// runtime that will execute it. Instructions is the worker's durable role and
// Task is this session's request; an adapter with no persistent worker may
// combine the two on the wire, but may not reorder them or change what they
// mean.
type SessionSpec struct {
	Model        ModelSelector   `json:"model"`
	Instructions string          `json:"instructions"`
	Task         string          `json:"task"`
	Sandbox      SandboxSpec     `json:"sandbox"`
	Resources    []Resource      `json:"resources,omitempty"`
	Evaluation   *EvaluationSpec `json:"evaluation,omitempty"`
}

// SessionRef is the handle to a session: everything needed to poll it, read its
// evidence, cancel it, and release it. Runtime and ID are fixed for the
// session's life and Revision only ever increases, so a checkpoint can be
// persisted with compare-and-swap.
//
// How long the handle stays meaningful is the runtime's to declare, not this
// contract's to promise. A runtime reporting Capabilities.Resumable may be
// handed a reference reconstructed in a later process and will still find the
// session; one reporting false will not, and the reference is good only inside
// the process that started the session. A caller that must survive a restart
// checks the capability at admission rather than discovering the difference
// when a restart happens.
//
// Locator is adapter-owned recovery metadata that a caller stores and returns
// byte for byte but never parses; LocatorVersion versions its shape, and it is
// size-limited and forbidden from carrying credentials or user content.
type SessionRef struct {
	Runtime        RuntimeName     `json:"runtime"`
	ID             string          `json:"id"`
	Revision       uint64          `json:"revision"`
	LocatorVersion int             `json:"locator_version"`
	Locator        json.RawMessage `json:"locator,omitempty"`
}

// Verdict is the evaluator's decision about a graded session. Explanation is
// the reasoning a reader is shown alongside the grade; a runtime that gives
// none leaves it empty rather than inventing one.
type Verdict struct {
	Grade       Grade  `json:"grade"`
	Explanation string `json:"explanation,omitempty"`
}

// Usage is the cumulative, provider-neutral cost of a session so far. The six
// token buckets are mutually exclusive so each is priced exactly once; Extra
// holds billable non-token meters, and Details holds non-billable decomposition
// such as reasoning tokens already counted in Output, which pricing never
// iterates. Anything the runtime did not report is zero, never an estimate
// dressed up as provider data.
//
// Usage is a running total for the whole session, not a delta for one call. A
// caller accumulates observations by replacement, so a runtime reporting deltas
// would be billed several times over; ValidateObservation refuses a total that
// decreased for exactly that reason.
type Usage struct {
	Model ModelRef `json:"model"`
	// InputFresh is prompt tokens the provider processed from scratch.
	InputFresh int64 `json:"input_fresh"`
	// InputCached is prompt tokens served from the provider's cache rather than
	// processed fresh. It is priced far below InputFresh, which is why it is a
	// bucket of its own rather than folded into it.
	InputCached int64 `json:"input_cached"`
	// CacheWrite is cache-write tokens whose bought lifetime the runtime did not
	// report. It is kept separate from the two known lifetimes so that "we do not
	// know which" stays visible rather than being guessed into a cheaper bucket.
	CacheWrite int64 `json:"cache_write"`
	// CacheWrite5m is cache-write tokens that bought the shorter cache lifetime.
	CacheWrite5m int64 `json:"cache_write_5m"`
	// CacheWrite1h is cache-write tokens that bought the longer cache lifetime. It
	// costs more than the shorter one, so folding the two together loses money in
	// one direction and overcharges in the other.
	CacheWrite1h int64 `json:"cache_write_1h"`
	// Output is tokens the model produced.
	Output int64 `json:"output"`
	// Extra holds billable non-token meters, such as a container second or a
	// hosted tool call.
	Extra map[Meter]int64 `json:"extra,omitempty"`
	// Details holds non-billable decomposition of the buckets above, such as
	// reasoning tokens already counted in Output. Pricing never iterates it, so a
	// new entry here can never change a bill.
	Details map[string]int64 `json:"details,omitempty"`
}

// Status is one observation of a session: where it has got to, what it has cost
// so far, and once terminal, why it stopped. Between observations only the
// reference's Revision and Locator may advance, state moves forwards only, and
// a terminal observation stays terminal however often the session is polled. An
// ungraded session finishes with no Verdict at all; a graded one may not finish
// without a decided verdict.
type Status struct {
	Ref            SessionRef     `json:"ref"`
	Model          ModelRef       `json:"model"`
	State          State          `json:"state"`
	StartedAt      time.Time      `json:"started_at"`
	FinishedAt     time.Time      `json:"finished_at"`
	Usage          Usage          `json:"usage"`
	TerminalReason TerminalReason `json:"terminal_reason,omitempty"`
	Error          string         `json:"error,omitempty"`
	Verdict        *Verdict       `json:"verdict,omitempty"`
}

// Event is one entry in a session's transcript, normalized across runtimes. ID
// is stable, so re-reading the transcript neither duplicates entries nor
// changes what an existing one means. Raw keeps the runtime's own payload
// intact, because the transcript is the evidence for a run and must not be
// reduced to this contract's vocabulary.
type Event struct {
	ID      string          `json:"id"`
	Role    Role            `json:"role"`
	Kind    EventKind       `json:"kind"`
	Tool    string          `json:"tool,omitempty"`
	Content string          `json:"content,omitempty"`
	Time    time.Time       `json:"time"`
	Usage   *Usage          `json:"usage,omitempty"`
	Raw     json.RawMessage `json:"raw,omitempty"`
}

// Artifact is one file the session produced. Path is a clean, relative,
// slash-separated path, unique within the session, and is what a reader sees;
// ID is the runtime's own handle for the same file and is passed back when
// fetching it, since not every backend addresses a file by path.
type Artifact struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// Capabilities is what a runtime can actually do, stated as semantic flags and
// limits rather than vendor product names. It exists so a specification is
// refused before anything remote is created: there is no best-effort mode in
// which an unsupported package manager, network mode, or oversized attachment
// is silently dropped.
type Capabilities struct {
	// OutputDir is the sandbox path a session is told to write its artifacts to. A
	// caller templates it into the instructions rather than hard-coding one
	// backend's filesystem layout.
	OutputDir string `json:"output_dir,omitempty"`
	// PackageManagers are the managers this runtime can install from, keyed by the
	// name a Package carries.
	PackageManagers map[string]bool `json:"package_managers,omitempty"`
	// NetworkModes are the egress policies this runtime can impose. A mode absent
	// here is refused, never downgraded to a stricter one that happens to be
	// supported.
	NetworkModes map[NetworkMode]bool `json:"network_modes,omitempty"`
	// RepositoryModes are the repository access modes this runtime can arrange.
	RepositoryModes map[AccessMode]bool `json:"repository_modes,omitempty"`
	// Evaluation reports whether this runtime can grade a session against a
	// rubric. When false, a specification carrying an EvaluationSpec is refused:
	// grading is not something a caller can bolt on afterwards from a transcript.
	Evaluation bool `json:"evaluation,omitempty"`
	// DurableEvents reports whether the transcript is stored by the runtime rather
	// than buffered in this process. When false the evidence dies with the
	// process, so a caller that wants to keep it must read it before letting the
	// process end.
	DurableEvents bool `json:"durable_events,omitempty"`
	// Artifacts reports whether the runtime can enumerate the files a session
	// produced. When false, ListArtifacts refuses rather than returning an empty
	// list, which would read as "the session produced nothing".
	Artifacts bool `json:"artifacts,omitempty"`
	// Cancellation reports whether a running session can be stopped. When false,
	// Cancel refuses and a caller's only lever is to stop reading.
	Cancellation bool `json:"cancellation,omitempty"`
	// Resumable reports whether a SessionRef survives the process that created it.
	// When true, a reference persisted now can be handed back to a fresh process
	// later and will still name a live session. When false the reference is
	// meaningful only inside the process that started the session: an adapter over
	// an in-process runtime declares false, and a caller that must survive a
	// restart refuses the runtime at admission rather than discovering the loss
	// when a restart happens.
	//
	// This is separate from DurableEvents on purpose. That flag is about the
	// evidence outliving the process; this one is about the handle doing so. A
	// runtime can lose one and keep the other.
	Resumable bool `json:"resumable,omitempty"`
	// MaxAttachmentBytes bounds one attachment's content. Zero means the runtime
	// declares no bound.
	MaxAttachmentBytes int64 `json:"max_attachment_bytes,omitempty"`
	// MaxLocatorBytes bounds the locator this runtime writes into a reference.
	// Zero means DefaultMaxLocatorBytes.
	MaxLocatorBytes int `json:"max_locator_bytes,omitempty"`
}
