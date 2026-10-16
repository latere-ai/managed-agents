// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package latere runs a managed session as a hosted session on the Latere
// platform: Topos runs the agent and keeps the session, its machine and its
// model calls, and the person who starts the session pays for it from their
// own Latere wallet. The adapter is a client of the platform's API and holds
// no provider key.
package latere

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	managed "latere.ai/x/managed-agents"
)

// Name is the runtime's canonical name, the value every reference it issues
// carries.
const Name managed.RuntimeName = "latere"

// OutputDir is the session's working directory on its machine, which is
// where a session writes what it produces.
const OutputDir = "/workspace"

// DefaultBaseURL is the platform's API origin.
const DefaultBaseURL = "https://api.latere.ai"

// DefaultImage is the machine image a session runs on: the platform's base
// image, with Python and its document and data libraries.
const DefaultImage = "base"

// DefaultImagePackages are the pip packages the base image provides. A
// session asking for any other is refused rather than run without it, since
// the platform has no field that installs one.
var DefaultImagePackages = []string{
	"beautifulsoup4", "lxml", "matplotlib", "numpy", "openpyxl", "pandas", "pillow",
	"pypdf", "python-docx", "pyyaml", "reportlab", "requests",
}

// The Effort levels, by the preference a session asks for. The stored
// preferences keep their old names; the platform's levels are the family's
// words for the same three choices.
var levels = map[string]string{"fast": "quick", "balanced": "thorough", "best": "sustained"}

// Product names the application a runtime's sessions belong to, as the
// person sees them in the Latere console. Key is a lowercase word of letters,
// digits and hyphens: agents are named "<key>-<digest>", sessions carry it as
// their product metadata, and a cancellation is recorded under
// "<key>.cancelled". Name is how the console labels the agent and the
// session.
type Product struct {
	Key  string
	Name string
}

// DefaultProduct is the product a Config that names none runs under.
var DefaultProduct = Product{Key: "managed", Name: "Managed session"}

// cancelKey is the session metadata entry Cancel writes, so every process
// polling the session sees the cancellation at once, before the platform has
// stopped the turn.
func (p Product) cancelKey() string { return p.Key + ".cancelled" }

func (p Product) valid() bool {
	if p.Key == "" || strings.TrimSpace(p.Name) == "" || len(p.Key) > 40 {
		return false
	}
	for _, c := range p.Key {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return p.Key[0] != '-' && p.Key[len(p.Key)-1] != '-'
}

// Config is what a caller hands this package.
type Config struct {
	// BaseURL is the platform's API origin; empty is DefaultBaseURL.
	BaseURL string
	// Product names the application the sessions belong to; the zero value
	// is DefaultProduct.
	Product Product
	// Tokens acts for the person when a call's context carries no source
	// of its own. A server leaves it nil and puts each session's source on
	// the context (WithTokenSource).
	Tokens TokenSource
	// Image and Packages describe the machine: the image a session runs
	// on and the pip packages it already provides. Empty is the base
	// image and its packages.
	Image    string
	Packages []string
	// HTTP is the client used for every call; nil is a plain client with a
	// timeout. A caller that traces its outbound calls passes its own.
	HTTP *http.Client
}

// Runtime is the Latere platform behind the managed-session contract.
type Runtime struct {
	platform platform
	product  Product
	tokens   TokenSource
	image    string
	packages []string
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error

	mu      sync.Mutex
	network map[managed.NetworkMode]bool
	logs    map[string][]wireEvent
}

// New builds the runtime. It reaches nothing: credentials are asked for on
// each call.
func New(config Config) (*Runtime, error) {
	base := strings.TrimRight(config.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	if !strings.HasPrefix(base, "https://") && !strings.HasPrefix(base, "http://") {
		return nil, fmt.Errorf("the Latere API address %q is not an http or https URL", config.BaseURL)
	}
	product := config.Product
	if product == (Product{}) {
		product = DefaultProduct
	}
	if !product.valid() {
		return nil, fmt.Errorf("the product %q is not a lowercase word of letters, digits and hyphens with a name", product.Key)
	}
	httpClient := config.HTTP
	if httpClient == nil {
		httpClient = defaultHTTPClient()
	}
	image := config.Image
	if image == "" {
		image = DefaultImage
	}
	packages := config.Packages
	if len(packages) == 0 {
		packages = DefaultImagePackages
	}
	return &Runtime{
		platform: platform{client: &client{base: base, http: httpClient}},
		product:  product, tokens: config.Tokens, image: image, packages: packages,
		now: time.Now, sleep: sleepContext, logs: map[string][]wireEvent{},
	}, nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Name is the canonical runtime name.
func (r *Runtime) Name() managed.RuntimeName { return Name }

// bearer is the token the call's person acts with: the context's source
// when there is one, then the runtime's own.
func (r *Runtime) bearer(ctx context.Context) (string, error) {
	source, ok := tokenSourceFrom(ctx)
	if !ok {
		source = r.tokens
	}
	if source == nil {
		return "", ErrNoCredential
	}
	return source.Token(ctx)
}

// Capabilities reads the person's network preference, which is the only
// network a session of theirs can have, and declares exactly that mode.
func (r *Runtime) Capabilities(ctx context.Context) (managed.Capabilities, error) {
	bearer, err := r.bearer(ctx)
	if err != nil {
		return managed.Capabilities{}, err
	}
	preference, err := r.platform.networkPreference(ctx, bearer)
	if err != nil {
		return managed.Capabilities{}, fmt.Errorf("read the Latere network preference: %w", err)
	}
	modes := map[managed.NetworkMode]bool{}
	if mode, ok := preferenceMode(preference); ok {
		modes[mode] = true
	}
	r.mu.Lock()
	r.network = modes
	r.mu.Unlock()
	capabilities := r.capabilities()
	capabilities.NetworkModes = modes
	return capabilities, nil
}

// preferenceMode is the network mode a person's preference gives a session.
// Open is open. A listed, package-manager or asking preference is an
// allowlist the platform chooses and widens with its own hosts, which is
// not a network a session can ask for, so it gives none of the contract's
// modes. A none preference is not offered by the platform today; it is read
// as none for a deployment that offers it.
func preferenceMode(preference string) (managed.NetworkMode, bool) {
	switch preference {
	case "open":
		return managed.NetworkOpen, true
	case "none":
		return managed.NetworkNone, true
	}
	return "", false
}

// capabilities is the declaration with no network read: Validate is pure and
// may not reach the platform, so it checks the network against what the
// last Capabilities read, or against the two modes a preference can give
// when nothing was read yet. Start reads the preference again before any
// remote write.
func (r *Runtime) capabilities() managed.Capabilities {
	packages := map[string]bool{}
	if len(r.packages) > 0 {
		packages["pip"] = true
	}
	r.mu.Lock()
	network := r.network
	r.mu.Unlock()
	if network == nil {
		network = map[managed.NetworkMode]bool{managed.NetworkOpen: true, managed.NetworkNone: true}
	}
	return managed.Capabilities{
		OutputDir:       OutputDir,
		PackageManagers: packages,
		NetworkModes:    network,
		RepositoryModes: map[managed.AccessMode]bool{},
		Evaluation:      true,
		DurableEvents:   true,
		Artifacts:       true,
		Cancellation:    true,
		// The platform keeps the session; the reference names it by id, so
		// any process holding the person's credential reaches it again.
		Resumable:       true,
		MaxLocatorBytes: managed.DefaultMaxLocatorBytes,
	}
}

// Models lists the three Effort levels, each serving its preference, and
// every model the person may name. A level's reference names the level
// itself; the model it resolves to is reported in Status. The list is the
// person's own, so it is read for every call: two people may be offered
// different catalogs.
func (r *Runtime) Models(ctx context.Context) ([]managed.Model, error) {
	bearer, err := r.bearer(ctx)
	if err != nil {
		return nil, err
	}
	catalog, err := r.platform.catalog(ctx, bearer)
	if err != nil {
		return nil, fmt.Errorf("read the Latere model catalog: %w", err)
	}
	models := make([]managed.Model, 0, len(catalog)+len(levels))
	for _, preference := range []string{"best", "balanced", "fast"} {
		models = append(models, managed.Model{Ref: modelRef("tier/" + levels[preference]), Preferences: []string{preference}})
	}
	for _, entry := range catalog {
		if entry.Spec.Disabled || !entry.Status.Available {
			continue
		}
		models = append(models, managed.Model{Ref: modelRef(entry.Metadata.Name)})
	}
	return models, nil
}

// Effort is one Effort level as the person asking would get it now: the
// model its next turn runs on and that model's price in micro-USD per
// million tokens, zero when the turn would be free.
type Effort struct {
	Preference string
	Level      string
	Model      managed.ModelRef
	Input      int64
	Output     int64
	Available  bool
}

// Efforts reads the person's three Effort levels.
func (r *Runtime) Efforts(ctx context.Context) ([]Effort, error) {
	bearer, err := r.bearer(ctx)
	if err != nil {
		return nil, err
	}
	effort, err := r.platform.effort(ctx, bearer)
	if err != nil {
		return nil, fmt.Errorf("read the Latere Effort levels: %w", err)
	}
	out := make([]Effort, 0, len(levels))
	for _, preference := range []string{"best", "balanced", "fast"} {
		e := Effort{Preference: preference, Level: levels[preference]}
		for _, level := range effort.Levels {
			if level.ID != e.Level {
				continue
			}
			e.Available = level.Available
			for _, model := range level.Models {
				if model.Available {
					e.Model = modelRef(model.Name)
					break
				}
			}
			if price := level.Next.Price; price != nil {
				e.Input, e.Output = perMillion(price.Input, price.Per), perMillion(price.Output, price.Per)
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// perMillion turns a decimal USD price per per tokens into micro-USD per
// million tokens. An amount that does not parse reads as unknown, zero.
func perMillion(amount string, per int64) int64 {
	if per <= 0 {
		return 0
	}
	whole, fraction, _ := strings.Cut(strings.TrimSpace(amount), ".")
	fraction = (fraction + "000000")[:6]
	micro, err := strconv.ParseInt(whole+fraction, 10, 64)
	if err != nil || micro < 0 {
		return 0
	}
	return micro * 1_000_000 / per
}

// Validate refuses what the platform cannot do, with no remote call.
func (r *Runtime) Validate(spec managed.SessionSpec) error {
	if err := managed.ValidateCapabilities(Name, r.capabilities(), spec); err != nil {
		return err
	}
	for i, pkg := range spec.Sandbox.Packages {
		if !slices.Contains(r.packages, strings.ToLower(pkg.Name)) || pkg.Version != "" {
			return &managed.UnsupportedError{Runtime: Name, Field: fmt.Sprintf("sandbox.packages[%d]", i), Capability: "image_package:" + pkg.Name}
		}
	}
	if len(spec.Sandbox.Bootstrap) > 0 {
		return &managed.UnsupportedError{Runtime: Name, Field: "sandbox.bootstrap", Capability: "bootstrap"}
	}
	for i, resource := range spec.Resources {
		if resource.Kind == managed.ResourceAttachment {
			// The platform writes an attachment under attachments/<event>/,
			// not at the path the specification names.
			return &managed.UnsupportedError{Runtime: Name, Field: fmt.Sprintf("resources[%d].attachment", i), Capability: "attachment_path"}
		}
	}
	if spec.Model.Preference != "" {
		if _, ok := levels[spec.Model.Preference]; !ok {
			return &managed.InvalidSpecError{Field: "model.preference", Message: fmt.Sprintf("%q is not one of best, balanced, fast", spec.Model.Preference)}
		}
	}
	return nil
}

// locator is what the adapter needs to find a session again: which agent
// version it runs, the model it started on, whether and how it is graded,
// and the event that carried its task. It never changes after Start. Where
// the grading loop stands is read from the session's own log on every poll,
// so a reference from any moment, a stale one included, reaches the same
// state, and a poll replayed after a crash cannot repeat a step. It holds no
// credential and no text the session was given or wrote.
type locator struct {
	Agent        string    `json:"agent"`
	Model        string    `json:"model"`
	Graded       bool      `json:"graded"`
	MaxRevisions int       `json:"max_revisions,omitempty"`
	TaskSeq      uint64    `json:"task_seq"`
	Started      time.Time `json:"started"`
}

// LocatorVersion is the schema of the locator.
const LocatorVersion = 1

// The stages of the grading loop, as the log shows them.
const (
	stageWork   = "work"
	stageGrade  = "grade"
	stageRevise = "revise"
)

// Start creates the worker agent and the session, sends the task, and
// returns once the session exists. A failure after the session was created
// deletes it before returning.
func (r *Runtime) Start(ctx context.Context, spec managed.SessionSpec) (managed.SessionRef, error) {
	if err := r.Validate(spec); err != nil {
		return managed.SessionRef{}, err
	}
	bearer, err := r.bearer(ctx)
	if err != nil {
		return managed.SessionRef{}, err
	}
	preference, err := r.platform.networkPreference(ctx, bearer)
	if err != nil {
		return managed.SessionRef{}, fmt.Errorf("read the Latere network preference: %w", err)
	}
	want := spec.Sandbox.Network.Mode
	if want == "" {
		want = managed.NetworkNone
	}
	if mode, ok := preferenceMode(preference); !ok || mode != want {
		return managed.SessionRef{}, &managed.UnsupportedError{Runtime: Name, Field: "sandbox.network.mode",
			Capability: fmt.Sprintf("network:%s (the Latere network preference is %q; set it to %s in the console, under Preferences)", want, preference, want)}
	}
	model, grader, err := r.sessionModels(ctx, bearer, spec)
	if err != nil {
		return managed.SessionRef{}, err
	}
	rubric := ""
	if spec.Evaluation != nil {
		rubric = spec.Evaluation.Rubric
	}
	manifest, err := workerManifest(r.product, spec.Instructions, r.image, model, grader, rubric)
	if err != nil {
		return managed.SessionRef{}, err
	}
	version, err := r.platform.applyAgent(ctx, bearer, manifest.Metadata.Name, manifest)
	if err != nil {
		return managed.SessionRef{}, fmt.Errorf("apply the session's agent: %w", err)
	}
	agent := fmt.Sprintf("%s@%d", manifest.Metadata.Name, version)
	nonce, err := randomID()
	if err != nil {
		return managed.SessionRef{}, err
	}
	created, err := r.platform.createSession(ctx, bearer, createSession{
		Agent: agent, Title: r.product.Name, Attended: false, Metadata: map[string]string{"product": r.product.Key},
	}, "create-"+nonce)
	if err != nil {
		return managed.SessionRef{}, fmt.Errorf("create the session: %w", err)
	}
	rollback := func(cause error) (managed.SessionRef, error) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		return managed.SessionRef{}, errors.Join(cause, r.platform.remove(cleanup, bearer, created.ID))
	}
	if got, ok := preferenceMode(created.Network.Mode); !ok || got != want {
		return rollback(&managed.UnsupportedError{Runtime: Name, Field: "sandbox.network.mode", Capability: "network:" + string(want)})
	}
	started := model
	if created.Model != nil && created.Model.Name != "" {
		started = created.Model.Name
	}
	sent, err := r.platform.message(ctx, bearer, created.ID, spec.Task, nil, created.ID+"-task")
	if err != nil {
		return rollback(fmt.Errorf("send the task: %w", err))
	}
	maxRevisions := 0
	if spec.Evaluation != nil {
		maxRevisions = spec.Evaluation.MaxRevisions
	}
	loc := locator{
		Agent: agent, Model: started, Graded: spec.Evaluation != nil, MaxRevisions: maxRevisions,
		TaskSeq: sent.Seq, Started: created.CreatedAt,
	}
	ref, err := reference(created.ID, 1, loc)
	if err != nil {
		return rollback(err)
	}
	return ref, nil
}

// sessionModels resolves the name the session starts on and, for a graded
// session, the model its grader runs on. A grader must name a model, not a
// level, and cannot name a free one, so on a level it runs on the level's
// first paid model.
func (r *Runtime) sessionModels(ctx context.Context, bearer string, spec managed.SessionSpec) (string, string, error) {
	var model string
	if spec.Model.Exact != nil {
		model = modelName(*spec.Model.Exact)
	} else {
		model = "tier/" + levels[spec.Model.Preference]
	}
	if spec.Evaluation == nil {
		return model, "", nil
	}
	level, ok := strings.CutPrefix(model, "tier/")
	if !ok {
		return model, strings.TrimPrefix(model, freeVariant+"/"), nil
	}
	effort, err := r.platform.effort(ctx, bearer)
	if err != nil {
		return "", "", fmt.Errorf("read the Latere Effort levels: %w", err)
	}
	for _, candidate := range effort.Levels {
		if candidate.ID != level {
			continue
		}
		for _, entry := range candidate.Models {
			if entry.Available && entry.Charge == "credit" && !strings.HasPrefix(entry.Name, freeVariant+"/") {
				return model, entry.Name, nil
			}
		}
	}
	return "", "", fmt.Errorf("the Latere Effort level %q offers no paid model a grader can run on", level)
}

func randomID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate an idempotency key: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func reference(id string, revision uint64, loc locator) (managed.SessionRef, error) {
	raw, err := json.Marshal(loc)
	if err != nil {
		return managed.SessionRef{}, fmt.Errorf("encode the session locator: %w", err)
	}
	ref := managed.SessionRef{Runtime: Name, ID: id, Revision: revision, LocatorVersion: LocatorVersion, Locator: raw}
	return ref, ref.Validate(managed.DefaultMaxLocatorBytes)
}

func decode(ref managed.SessionRef) (locator, error) {
	if ref.Runtime != Name {
		return locator{}, &managed.RuntimeMismatchError{Expected: Name, Actual: ref.Runtime}
	}
	if ref.LocatorVersion != LocatorVersion {
		return locator{}, fmt.Errorf("%w: locator version %d is not %d", managed.ErrContract, ref.LocatorVersion, LocatorVersion)
	}
	var loc locator
	if err := json.Unmarshal(ref.Locator, &loc); err != nil {
		return locator{}, fmt.Errorf("%w: decode the locator: %w", managed.ErrContract, err)
	}
	return loc, nil
}

// log reads the session's events, fetching only what is new since this
// process last read them.
func (r *Runtime) log(ctx context.Context, bearer, id string) ([]wireEvent, error) {
	r.mu.Lock()
	known := slices.Clone(r.logs[id])
	r.mu.Unlock()
	from := uint64(0)
	if len(known) > 0 {
		from = known[len(known)-1].Seq
	}
	fresh, err := r.platform.events(ctx, bearer, id, from)
	if err != nil {
		return nil, err
	}
	events := append(known, fresh...)
	r.mu.Lock()
	r.logs[id] = events
	r.mu.Unlock()
	return events, nil
}

// Poll observes the session and advances its grading loop: when the worker's
// turn ends it asks for a grade, when the grader answers it decides, and
// when a revision is due it sends one. Every message it sends carries an
// idempotency key made of the session, the stage and the round, and the
// stage is read back from the log, so a replayed poll resolves to the
// message the first one sent.
func (r *Runtime) Poll(ctx context.Context, ref managed.SessionRef) (managed.Status, error) {
	loc, err := decode(ref)
	if err != nil {
		return managed.Status{}, err
	}
	bearer, err := r.bearer(ctx)
	if err != nil {
		return managed.Status{}, err
	}
	session, err := r.platform.session(ctx, bearer, ref.ID)
	if err != nil {
		return managed.Status{}, err
	}
	events, err := r.log(ctx, bearer, ref.ID)
	if err != nil {
		return managed.Status{}, err
	}
	model := modelRef(loc.Model)
	status := managed.Status{Ref: ref, Model: model, State: managed.StateRunning, StartedAt: loc.Started}
	outcome, err := r.advance(ctx, bearer, ref.ID, session, events, loc)
	if err != nil {
		return managed.Status{}, err
	}
	if outcome.sent {
		// What advance sent belongs in this observation's usage too, so the
		// log is read again before the total is taken.
		if events, err = r.log(ctx, bearer, ref.ID); err != nil {
			return managed.Status{}, err
		}
	}
	status.Usage = total(normalize(events), model)
	if outcome.state.Terminal() {
		status.State, status.TerminalReason, status.FinishedAt = outcome.state, outcome.reason, outcome.at
		status.Error, status.Verdict = faults[outcome.fault], outcome.verdict
	}
	return status, nil
}

// The reasons a session fails other than its grade, each a sentence in the
// status.
var faults = map[string]string{
	"budget":       "the platform stopped the session: the Latere wallet of the person who started it cannot pay for more",
	"turn_limit":   "the platform stopped the session: a turn ran past its time limit",
	"output_limit": "the platform stopped the session: the model's answer reached its output limit",
	"error":        "the platform stopped the session on an error; its transcript says which",
	"ended":        "the platform ended the session before it finished",
	"waiting":      "the session stopped to wait for a person, which an unattended session cannot do",
	"grading":      "the grader did not return a verdict",
}

// outcome is one poll's reading: a terminal state with its reason, or
// running, and whether the poll sent a message.
type outcome struct {
	state   managed.State
	reason  managed.TerminalReason
	fault   string
	at      time.Time
	verdict *managed.Verdict
	sent    bool
}

func ended(reason managed.TerminalReason, fault string, at time.Time) outcome {
	return outcome{state: managed.StateFailed, reason: reason, fault: fault, at: at}
}

// progress is where the grading loop stands, replayed from the log: the
// stage, the round, how many times the round has been graded, how many
// revisions were sent, and the event that began the stage.
type progress struct {
	stage     string
	round     int
	attempt   int
	revisions int
	since     uint64
}

// replay reads the grading loop's state from the messages the adapter sent,
// which it recognizes by their exact text. A message anyone else sent to
// the session is not one of them and changes nothing here.
func replay(events []wireEvent, loc locator) progress {
	p := progress{stage: stageWork, since: loc.TaskSeq}
	for _, event := range events {
		if event.Seq <= loc.TaskSeq || event.Type != "user.message" || event.Thread != "" {
			continue
		}
		var message wireUserMessage
		if json.Unmarshal(event.Payload, &message) != nil {
			continue
		}
		body := text(message.Content)
		switch {
		case body == spawnRequest(roundTask(p.round)):
			if p.stage == stageGrade {
				p.attempt++
			} else {
				p.stage, p.attempt = stageGrade, 1
			}
			p.since = event.Seq
		case strings.HasPrefix(body, revisionLead):
			p.stage, p.round, p.revisions, p.attempt, p.since = stageRevise, p.round+1, p.revisions+1, 0, event.Seq
		}
	}
	return p
}

// advance is the grading loop's one step.
func (r *Runtime) advance(ctx context.Context, bearer, id string, session wireSession, events []wireEvent, loc locator) (outcome, error) {
	if session.metadataCancelled(r.product) {
		return ended(managed.ReasonCancelled, "", cancelledAt(events, loc)), nil
	}
	p := replay(events, loc)
	stop, at, idle := idleAfter(events, p.since)
	if !idle {
		if session.Status == "ended" {
			return ended(managed.ReasonRuntimeFailed, "ended", session.UpdatedAt), nil
		}
		return outcome{state: managed.StateRunning}, nil
	}
	switch stop {
	case "end_turn":
	case "interrupted":
		return ended(managed.ReasonCancelled, "", at), nil
	case "budget", "turn_limit", "output_limit", "error":
		return ended(managed.ReasonRuntimeFailed, stop, at), nil
	default:
		return ended(managed.ReasonRuntimeFailed, "waiting", at), nil
	}
	if p.stage != stageGrade {
		if !loc.Graded {
			return outcome{state: managed.StateDone, reason: managed.ReasonSucceeded, at: at}, nil
		}
		return r.requestGrade(ctx, bearer, id, p.round, 1)
	}
	verdict, err := graderVerdict(events, p.since, roundTask(p.round))
	if err != nil {
		if p.attempt < graderAttempts {
			return r.requestGrade(ctx, bearer, id, p.round, p.attempt+1)
		}
		return ended(managed.ReasonRuntimeFailed, "grading", at), nil
	}
	switch {
	case verdict.Passed():
		return outcome{state: managed.StateDone, reason: managed.ReasonSucceeded, at: at, verdict: &verdict}, nil
	case verdict.Grade == managed.GradeNeedsRevision && p.revisions < loc.MaxRevisions:
		key := fmt.Sprintf("%s-revise-%d", id, p.round+1)
		if _, err := r.platform.message(ctx, bearer, id, revisionLead+verdict.Explanation, nil, key); err != nil {
			return outcome{}, fmt.Errorf("send revision %d: %w", p.round+1, err)
		}
		return outcome{state: managed.StateRunning, sent: true}, nil
	case verdict.Grade == managed.GradeNeedsRevision:
		verdict.Grade = managed.GradeExhausted
		fallthrough
	default:
		result := ended(managed.ReasonEvaluationFailed, "", at)
		result.verdict = &verdict
		return result, nil
	}
}

// cancelledAt is when a cancelled session stopped: its interruption, or,
// before the platform has recorded one, its last event.
func cancelledAt(events []wireEvent, loc locator) time.Time {
	var last time.Time
	for _, event := range events {
		if event.Seq > loc.TaskSeq && event.Type == "user.interrupt" {
			return event.Time
		}
		last = event.Time
	}
	return last
}

// idleAfter finds the first time the session's own thread went idle after
// sequence since, and why.
func idleAfter(events []wireEvent, since uint64) (string, time.Time, bool) {
	for _, event := range events {
		if event.Seq <= since || event.Type != "session.status" || event.Thread != "" {
			continue
		}
		var status wireStatus
		if json.Unmarshal(event.Payload, &status) == nil && status.Status == "idle" {
			return status.StopReason, event.Time, true
		}
	}
	return "", time.Time{}, false
}

// requestGrade asks the worker to spawn the grader for a round.
func (r *Runtime) requestGrade(ctx context.Context, bearer, id string, round, attempt int) (outcome, error) {
	key := fmt.Sprintf("%s-grade-%d-%d", id, round, attempt)
	if _, err := r.platform.message(ctx, bearer, id, spawnRequest(roundTask(round)), nil, key); err != nil {
		return outcome{}, fmt.Errorf("ask for round %d's grade: %w", round, err)
	}
	return outcome{state: managed.StateRunning, sent: true}, nil
}

// graderVerdict finds the grader thread started after since, checks it is
// the grader asked for, and reads the verdict from the thread's last
// message.
func graderVerdict(events []wireEvent, since uint64, task string) (managed.Verdict, error) {
	thread, model := "", managed.ModelRef{}
	for _, event := range events {
		if event.Seq <= since || event.Type != "thread.started" {
			continue
		}
		var started wireThreadStarted
		if err := json.Unmarshal(event.Payload, &started); err != nil {
			return managed.Verdict{}, fmt.Errorf("read the grader's start: %w", err)
		}
		if err := graderStarted(started, task); err != nil {
			return managed.Verdict{}, err
		}
		thread, model = event.Thread, modelRef(started.Model)
		break
	}
	if thread == "" {
		return managed.Verdict{}, errors.New("the worker did not start the grader")
	}
	var last *wireEvent
	for i := range events {
		if events[i].Seq > since && events[i].Thread == thread && events[i].Type == "agent.message" {
			last = &events[i]
		}
	}
	if last == nil {
		return managed.Verdict{}, errors.New("the grader wrote no message")
	}
	var message wireAgentMessage
	if err := json.Unmarshal(last.Payload, &message); err != nil {
		return managed.Verdict{}, fmt.Errorf("read the grader's message: %w", err)
	}
	return parseVerdict(text(message.Message.Blocks), model)
}

// Events is the session's transcript, normalized.
func (r *Runtime) Events(ctx context.Context, ref managed.SessionRef) ([]managed.Event, error) {
	if _, err := decode(ref); err != nil {
		return nil, err
	}
	bearer, err := r.bearer(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := r.platform.session(ctx, bearer, ref.ID); err != nil {
		return nil, err
	}
	events, err := r.log(ctx, bearer, ref.ID)
	if err != nil {
		return nil, err
	}
	return normalize(events), nil
}

// sessionFolder is the session's own folder in the person's Storage space,
// where Topos keeps a copy of its working directory under work/.
func sessionFolder(id string) string { return "files/conversations/" + id + "/work" }

// ListArtifacts lists the files the session left in its working directory,
// from the copy the platform keeps in Storage, so the list does not depend
// on the session's machine still running. The files the platform delivered
// into attachments/ are the session's inputs, not what it produced.
func (r *Runtime) ListArtifacts(ctx context.Context, ref managed.SessionRef) ([]managed.Artifact, error) {
	if _, err := decode(ref); err != nil {
		return nil, err
	}
	bearer, err := r.bearer(ctx)
	if err != nil {
		return nil, err
	}
	session, err := r.platform.session(ctx, bearer, ref.ID)
	if err != nil {
		return nil, err
	}
	folder := sessionFolder(ref.ID)
	entries, err := r.platform.storageList(ctx, bearer, session.Initiator.Subject, folder)
	if errors.Is(err, managed.ErrNotFound) {
		// A session that never changed a file has no copy yet.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list the session's files: %w", err)
	}
	artifacts := make([]managed.Artifact, 0, len(entries))
	for _, entry := range entries {
		relative, ok := strings.CutPrefix(entry.Path, folder+"/")
		if !ok || relative == "" || path.Clean(relative) != relative || strings.HasPrefix(relative, "attachments/") {
			continue
		}
		artifact := managed.Artifact{ID: relative, Path: relative}
		if artifact.IsCompiled() {
			continue
		}
		artifacts = append(artifacts, artifact)
	}
	slices.SortFunc(artifacts, func(a, b managed.Artifact) int { return strings.Compare(a.Path, b.Path) })
	return artifacts, nil
}

// FetchArtifact reads one listed file. A file the listing does not hold is
// refused, so a caller cannot read another path of the person's Storage.
func (r *Runtime) FetchArtifact(ctx context.Context, ref managed.SessionRef, artifact managed.Artifact) ([]byte, error) {
	listed, err := r.ListArtifacts(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(listed, func(candidate managed.Artifact) bool { return candidate.Path == artifact.Path }) {
		return nil, &managed.NotFoundError{Runtime: Name, ID: artifact.Path}
	}
	bearer, err := r.bearer(ctx)
	if err != nil {
		return nil, err
	}
	session, err := r.platform.session(ctx, bearer, ref.ID)
	if err != nil {
		return nil, err
	}
	return r.platform.storageRead(ctx, bearer, session.Initiator.Subject, sessionFolder(ref.ID)+"/"+artifact.Path)
}

// Cancel marks the session cancelled, so every later poll reports it so at
// once, and interrupts the running turn. A session already gone is success.
func (r *Runtime) Cancel(ctx context.Context, ref managed.SessionRef) error {
	if _, err := decode(ref); err != nil {
		return err
	}
	bearer, err := r.bearer(ctx)
	if err != nil {
		return err
	}
	if err := r.platform.markCancelled(ctx, bearer, ref.ID, r.product); err != nil {
		if errors.Is(err, managed.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("mark the session cancelled: %w", err)
	}
	if err := r.platform.interrupt(ctx, bearer, ref.ID); err != nil && !errors.Is(err, managed.ErrNotFound) {
		return fmt.Errorf("interrupt the session: %w", err)
	}
	return nil
}

// releaseAttempts bounds how long Release waits for an interrupted turn to
// stop before it can delete the session.
const releaseAttempts = 30

// Release deletes the session, which removes its machine and its folder. The
// platform refuses to delete a running session, so a running one is
// interrupted and waited for. A session already gone is success.
func (r *Runtime) Release(ctx context.Context, ref managed.SessionRef) error {
	if _, err := decode(ref); err != nil {
		return err
	}
	bearer, err := r.bearer(ctx)
	if err != nil {
		return err
	}
	for attempt := range releaseAttempts {
		err := r.platform.remove(ctx, bearer, ref.ID)
		switch {
		case err == nil, errors.Is(err, managed.ErrNotFound):
			r.mu.Lock()
			delete(r.logs, ref.ID)
			r.mu.Unlock()
			return nil
		case code(err) != "conflict":
			return fmt.Errorf("delete the session: %w", err)
		}
		if attempt == 0 {
			if err := r.platform.interrupt(ctx, bearer, ref.ID); err != nil && !errors.Is(err, managed.ErrNotFound) {
				return fmt.Errorf("interrupt the session before deleting it: %w", err)
			}
		}
		if err := r.sleep(ctx, 2*time.Second); err != nil {
			return err
		}
	}
	return errors.New("the session kept running and could not be deleted")
}

var _ managed.Runtime = (*Runtime)(nil)
