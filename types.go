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
// Keeping attachments outside every repository destination is structural
// rather than stylistic: it is what makes "this data is not ours to
// redistribute" enforceable by the filesystem layout, not by the model's good
// behaviour.
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

// EvaluationSpec makes a session graded. A nil *EvaluationSpec on SessionSpec
// is the only way to say "ungraded", which is why a present one must carry a
// non-empty rubric and a positive revision budget: a zero value must not be
// readable as grading against nothing.
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
// package's to promise. A runtime reporting Capabilities.Resumable may be
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

// Meter names a billable quantity that is not a token bucket, such as a
// container second or a hosted tool call. The set is open because each runtime
// emits its own, but a meter must be priced before a session using it can
// settle.
type Meter string

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
	Model        ModelRef         `json:"model"`
	InputFresh   int64            `json:"input_fresh"`
	InputCached  int64            `json:"input_cached"`
	CacheWrite   int64            `json:"cache_write"`
	CacheWrite5m int64            `json:"cache_write_5m"`
	CacheWrite1h int64            `json:"cache_write_1h"`
	Output       int64            `json:"output"`
	Extra        map[Meter]int64  `json:"extra,omitempty"`
	Details      map[string]int64 `json:"details,omitempty"`
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

// Event is one entry in a session's transcript, normalised across runtimes. ID
// is stable, so re-reading the transcript neither duplicates entries nor
// changes what an existing one means. Raw keeps the runtime's own payload
// intact, because the transcript is the evidence for a run and must not be
// reduced to this package's vocabulary.
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
