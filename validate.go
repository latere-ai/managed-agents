// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
)

// DefaultMaxLocatorBytes is the locator bound callers pass when they have no
// runtime-specific limit to hand, and the one SessionRef.Validate substitutes
// when it is given none. A locator is adapter-owned recovery metadata that core
// code stores verbatim, so a bound is what stops an adapter turning the
// checkpoint into a payload store.
const DefaultMaxLocatorBytes = 64 << 10

var (
	preferencePattern = regexp.MustCompile(`^(best|balanced|fast)$`)
	packagePart       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]*$`)
	envNamePattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// runtimeNamePattern is the same expression the schema states for
	// RuntimeName. Canonical form alone was a weaker test — "my runtime" is
	// its own lower-cased, trimmed self — so Go accepted names the wire
	// contract rejects, and the two are supposed to be one contract.
	runtimeNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
)

func invalid(field, message string) error { return &InvalidSpecError{Field: field, Message: message} }

// Validate reports whether the specification is complete and consistent on its
// own terms: one model form, a task and instructions, a sandbox whose network
// mode agrees with its domains and secrets, and resources whose destinations do
// not collide. It asks nothing about a runtime — that is ValidateCapabilities —
// and it is pure and cheap, so every adapter can repeat it before a remote
// write rather than trusting the caller to have run it.
func (s SessionSpec) Validate() error {
	pref := strings.TrimSpace(s.Model.Preference)
	if (pref == "") == (s.Model.Exact == nil) {
		return invalid("model", "set exactly one of preference or exact")
	}
	if pref != "" && !preferencePattern.MatchString(pref) {
		return invalid("model.preference", "must be best, balanced, or fast")
	}
	if s.Model.Exact != nil && !s.Model.Exact.Valid() {
		return invalid("model.exact", "provider and id are required")
	}
	if strings.TrimSpace(s.Instructions) == "" {
		return invalid("instructions", "must not be empty")
	}
	if strings.TrimSpace(s.Task) == "" {
		return invalid("task", "must not be empty")
	}
	if s.Evaluation != nil {
		if strings.TrimSpace(s.Evaluation.Rubric) == "" {
			return invalid("evaluation.rubric", "must not be empty")
		}
		if s.Evaluation.MaxRevisions < 0 {
			return invalid("evaluation.max_revisions", "must not be negative")
		}
		if s.Evaluation.Findings != "" {
			if err := safeRelativePath(s.Evaluation.Findings); err != nil {
				return invalid("evaluation.findings", err.Error())
			}
		}
	}
	if err := s.Sandbox.validate(); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(s.Resources))
	for i, resource := range s.Resources {
		destination, err := resource.validate(i)
		if err != nil {
			return err
		}
		if _, exists := seen[destination]; exists {
			return invalid(fmt.Sprintf("resources[%d]", i), "duplicate destination "+destination)
		}
		seen[destination] = struct{}{}
	}
	return nil
}

func (s SandboxSpec) validate() error {
	for i, pkg := range s.Packages {
		if !packagePart.MatchString(pkg.Manager) {
			return invalid(fmt.Sprintf("sandbox.packages[%d].manager", i), "invalid package manager")
		}
		if !packagePart.MatchString(pkg.Name) {
			return invalid(fmt.Sprintf("sandbox.packages[%d].name", i), "invalid package name")
		}
		if pkg.Version != "" && !packagePart.MatchString(pkg.Version) {
			return invalid(fmt.Sprintf("sandbox.packages[%d].version", i), "invalid package version")
		}
	}
	for i, argv := range s.Bootstrap {
		if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
			return invalid(fmt.Sprintf("sandbox.bootstrap[%d]", i), "argv must contain an executable")
		}
		for _, arg := range argv {
			if strings.ContainsRune(arg, 0) {
				return invalid(fmt.Sprintf("sandbox.bootstrap[%d]", i), "argv contains NUL")
			}
		}
	}
	mode := s.Network.Mode
	if mode == "" {
		mode = NetworkNone
	}
	switch mode {
	case NetworkNone:
		if len(s.Network.AllowedDomains) > 0 || len(s.Network.SecretRefs) > 0 {
			return invalid("sandbox.network", "none cannot carry domains or secrets")
		}
	case NetworkAllowlist:
		if len(s.Network.AllowedDomains) == 0 {
			return invalid("sandbox.network.allowed_domains", "allowlist requires a domain")
		}
	case NetworkOpen:
		if len(s.Network.AllowedDomains) > 0 || len(s.Network.SecretRefs) > 0 {
			return invalid("sandbox.network", "open cannot carry domains or host-bound secrets")
		}
	default:
		return invalid("sandbox.network.mode", "unknown network mode")
	}
	allowed := make(map[string]struct{}, len(s.Network.AllowedDomains))
	for i, domain := range s.Network.AllowedDomains {
		canonical, err := canonicalDomain(domain)
		if err != nil || canonical != domain {
			return invalid(fmt.Sprintf("sandbox.network.allowed_domains[%d]", i), "must be a canonical hostname")
		}
		allowed[domain] = struct{}{}
	}
	for envName, ref := range s.Network.SecretRefs {
		if !envNamePattern.MatchString(envName) || strings.TrimSpace(ref) == "" {
			return invalid("sandbox.network.secret_refs", "invalid environment name or empty reference")
		}
		domain, _, ok := strings.Cut(ref, ":")
		if !ok {
			return invalid("sandbox.network.secret_refs", "reference must be domain:name")
		}
		if _, ok := allowed[domain]; !ok {
			return invalid("sandbox.network.secret_refs", "secret domain is not allowlisted")
		}
	}
	return nil
}

func canonicalDomain(raw string) (string, error) {
	if raw == "" || strings.ContainsAny(raw, "/:@") {
		return "", fmt.Errorf("not a hostname")
	}
	host := strings.ToLower(strings.TrimSuffix(raw, "."))
	if net.ParseIP(host) != nil {
		return "", fmt.Errorf("IP addresses are not domains")
	}
	u, err := url.Parse("https://" + host)
	if err != nil || u.Hostname() != host || !strings.Contains(host, ".") {
		return "", fmt.Errorf("invalid hostname")
	}
	return host, nil
}

func (r Resource) validate(index int) (string, error) {
	field := fmt.Sprintf("resources[%d]", index)
	switch r.Kind {
	case ResourceAttachment:
		if r.Attachment == nil || r.Repository != nil || r.Input != nil {
			return "", invalid(field, "attachment payload mismatch")
		}
		if err := checkFile(field+".attachment", r.Attachment.Path, r.Attachment.Content, r.Attachment.SHA256); err != nil {
			return "", err
		}
		return path.Clean(r.Attachment.Path), nil
	case ResourceInput:
		if r.Input == nil || r.Attachment != nil || r.Repository != nil {
			return "", invalid(field, "input payload mismatch")
		}
		if err := checkFile(field+".input", r.Input.Path, r.Input.Content, r.Input.SHA256); err != nil {
			return "", err
		}
		// An input lives in its own directory, so its destination cannot
		// collide with a path in the workspace.
		return "input:" + path.Clean(r.Input.Path), nil
	case ResourceRepository:
		if r.Repository == nil || r.Attachment != nil || r.Input != nil {
			return "", invalid(field, "repository payload mismatch")
		}
		u, err := url.Parse(r.Repository.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return "", invalid(field+".repository.url", "must be an HTTPS URL")
		}
		if err := safeRelativePath(r.Repository.Destination); err != nil {
			return "", invalid(field+".repository.destination", err.Error())
		}
		if r.Repository.Access != AccessReadOnly && r.Repository.Access != AccessReadWrite {
			return "", invalid(field+".repository.access", "must be ro or rw")
		}
		if r.Repository.Access == AccessReadWrite && strings.TrimSpace(r.Repository.CredentialRef) == "" {
			return "", invalid(field+".repository.credential_ref", "read-write repository requires a credential reference")
		}
		return path.Clean(r.Repository.Destination), nil
	default:
		return "", invalid(field+".kind", "unknown resource kind")
	}
}

// checkFile validates a file carried inline: a contained relative path, and a
// SHA-256 that matches the content, so a corrupted or substituted payload
// fails the specification instead of the run.
func checkFile(field, relative string, content []byte, sum string) error {
	if err := safeRelativePath(relative); err != nil {
		return invalid(field+".path", err.Error())
	}
	digest, err := hex.DecodeString(sum)
	if err != nil || len(digest) != sha256.Size {
		return invalid(field+".sha256", "must be a SHA-256 hex digest")
	}
	actual := sha256.Sum256(content)
	if !strings.EqualFold(hex.EncodeToString(actual[:]), sum) {
		return invalid(field+".sha256", "content digest mismatch")
	}
	return nil
}

func safeRelativePath(value string) error {
	if value == "" || strings.ContainsAny(value, "\\\x00") || strings.HasPrefix(value, "/") {
		return fmt.Errorf("must be a non-empty relative slash path")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != value {
		return fmt.Errorf("must be a clean contained path")
	}
	return nil
}

// Validate reports whether the reference is well-formed: a canonical runtime, a non-empty id, a positive revision and locator version,
// and a locator within maxBytes (DefaultMaxLocatorBytes when maxBytes is not
// positive) that parses as JSON and carries no secret-bearing key. The locator
// travels through storage and logs untouched, so a credential inside it is
// refused at this boundary rather than later.
func (r SessionRef) Validate(maxBytes int) error {
	if r.Runtime != CanonicalRuntimeName(string(r.Runtime)) || !runtimeNamePattern.MatchString(string(r.Runtime)) {
		return invalid("ref.runtime", "must be a canonical lower-case name")
	}
	if strings.TrimSpace(r.ID) == "" {
		return invalid("ref.id", "must not be empty")
	}
	if r.Revision == 0 {
		return invalid("ref.revision", "must be positive")
	}
	if r.LocatorVersion <= 0 {
		return invalid("ref.locator_version", "must be positive")
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxLocatorBytes
	}
	if len(r.Locator) > maxBytes {
		return invalid("ref.locator", "exceeds maximum size")
	}
	if len(r.Locator) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(r.Locator, &value); err != nil {
		return invalid("ref.locator", "must be valid JSON")
	}
	if secretKey(value) {
		return invalid("ref.locator", "contains a secret-bearing key")
	}
	return nil
}

func secretKey(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			lower := strings.ToLower(key)
			for _, forbidden := range []string{"secret", "token", "password", "credential", "api_key", "apikey"} {
				if strings.Contains(lower, forbidden) {
					return true
				}
			}
			if secretKey(child) {
				return true
			}
		}
	case []any:
		if slices.ContainsFunc(typed, secretKey) {
			return true
		}
	}
	return false
}

// ValidateCapabilities refuses a specification the named runtime cannot
// execute, after first checking the specification is valid at all. It is the
// gate that keeps the contract honest: an unsupported package manager, network
// mode, evaluation request, repository access mode, or oversized attachment
// fails here, before anything remote is created, instead of being dropped and
// run around.
func ValidateCapabilities(runtime RuntimeName, capabilities Capabilities, spec SessionSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	for i, pkg := range spec.Sandbox.Packages {
		if !capabilities.PackageManagers[pkg.Manager] {
			return &UnsupportedError{Runtime: runtime, Field: fmt.Sprintf("sandbox.packages[%d].manager", i), Capability: "package_manager:" + pkg.Manager}
		}
	}
	mode := spec.Sandbox.Network.Mode
	if mode == "" {
		mode = NetworkNone
	}
	if !capabilities.NetworkModes[mode] {
		return &UnsupportedError{Runtime: runtime, Field: "sandbox.network.mode", Capability: "network:" + string(mode)}
	}
	if spec.Evaluation != nil && !capabilities.Evaluation {
		return &UnsupportedError{Runtime: runtime, Field: "evaluation", Capability: "evaluation"}
	}
	if spec.Evaluation != nil && spec.Evaluation.Findings != "" && !capabilities.FindingsRounds {
		return &UnsupportedError{Runtime: runtime, Field: "evaluation.findings", Capability: "findings_rounds"}
	}
	for i, resource := range spec.Resources {
		if resource.Kind == ResourceRepository && !capabilities.RepositoryModes[resource.Repository.Access] {
			return &UnsupportedError{Runtime: runtime, Field: fmt.Sprintf("resources[%d].repository.access", i), Capability: "repository:" + string(resource.Repository.Access)}
		}
		if resource.Kind == ResourceAttachment && capabilities.MaxAttachmentBytes > 0 && int64(len(resource.Attachment.Content)) > capabilities.MaxAttachmentBytes {
			return &UnsupportedError{Runtime: runtime, Field: fmt.Sprintf("resources[%d].attachment.content", i), Capability: "attachment_size"}
		}
		if resource.Kind == ResourceInput && capabilities.InputDir == "" {
			return &UnsupportedError{Runtime: runtime, Field: fmt.Sprintf("resources[%d].input", i), Capability: "input"}
		}
		if resource.Kind == ResourceInput && capabilities.MaxAttachmentBytes > 0 && int64(len(resource.Input.Content)) > capabilities.MaxAttachmentBytes {
			return &UnsupportedError{Runtime: runtime, Field: fmt.Sprintf("resources[%d].input.content", i), Capability: "attachment_size"}
		}
	}
	return nil
}

// Grading is what a caller knows about whether grading was requested for the
// session being observed. It is a third state rather than a bool because
// "nobody recorded the request" is genuinely different from "grading was not
// requested", and reading the first as the second turns a runtime that graded
// an unrecorded session into a contract violation.
type Grading uint8

// What a caller knows about the grading request.
const (
	// GradingUnknown is a session observed through a persisted reference,
	// which does not record whether the original specification asked for
	// grading. A verdict is still checked for coherence, but its absence is
	// not a violation and neither is its presence.
	GradingUnknown Grading = iota
	// GradingRequested is a session whose specification carried an
	// EvaluationSpec. It may not finish without a decided verdict.
	GradingRequested
	// GradingNotRequested is a session whose specification did not. It may not
	// finish carrying a verdict at all.
	GradingNotRequested
)

// ValidateObservation reports whether current is a legal successor to previous
// for the session named by expected, running on runtime. A zero previous means
// current is the first observation, and only the single-observation rules
// apply.
//
// This is the executable form of the rules a type signature cannot state: usage
// is a cumulative total and never decreases, state moves forwards only, a
// terminal observation stays terminal, the resolved model never changes, and a
// verdict is present exactly when grading was requested. An adapter that breaks
// one of them compiles and passes a happy-path test, so the check has to live
// where every caller and the conformance suite can reach it.
//
// It takes a RuntimeName rather than a Runtime because it reads only the name;
// requiring the interface would make it uncallable from a test holding nothing
// but data.
//
// Every failure unwraps to ErrContract. Nothing the runtime reports afterwards
// can be trusted, so a caller stops rather than absorbing the observation.
func ValidateObservation(expected SessionRef, runtime RuntimeName, previous, current Status, grading Grading) error {
	if current.Ref.Runtime != runtime {
		return fmt.Errorf("%w: %w", ErrContract, &RuntimeMismatchError{Expected: runtime, Actual: current.Ref.Runtime})
	}
	if err := current.Ref.Validate(0); err != nil {
		return fmt.Errorf("%w: %w", ErrContract, err)
	}
	if !current.Ref.SameIdentity(expected) || current.Ref.Revision < expected.Revision {
		return fmt.Errorf("%w: invalid session checkpoint", ErrContract)
	}
	if current.Ref.Revision == expected.Revision &&
		(current.Ref.LocatorVersion != expected.LocatorVersion ||
			!bytes.Equal(current.Ref.Locator, expected.Locator)) {
		return fmt.Errorf("%w: locator changed without a revision", ErrContract)
	}
	if !current.State.Valid() {
		return fmt.Errorf("%w: invalid state %s", ErrContract, current.State)
	}
	if !current.Model.Valid() {
		return fmt.Errorf("%w: status has no resolved model", ErrContract)
	}
	if previous.State.Valid() {
		if previous.State.Terminal() && current.State != previous.State {
			return fmt.Errorf("%w: terminal state changed", ErrContract)
		}
		if stateRank(current.State) < stateRank(previous.State) {
			return fmt.Errorf("%w: state regressed from %s to %s", ErrContract, previous.State, current.State)
		}
		if !current.Usage.MonotonicFrom(previous.Usage) {
			return fmt.Errorf("%w: usage regressed", ErrContract)
		}
		if previous.Model.Valid() && current.Model != previous.Model {
			return fmt.Errorf("%w: resolved model changed", ErrContract)
		}
	}
	if current.State != StateDone {
		return nil
	}
	switch grading {
	case GradingRequested:
		if current.Verdict == nil || !current.Verdict.Grade.Decided() {
			return fmt.Errorf("%w: graded session completed without a decided verdict", ErrContract)
		}
	case GradingNotRequested:
		if current.Verdict != nil {
			return fmt.Errorf("%w: ungraded session completed with a verdict", ErrContract)
		}
	case GradingUnknown:
		if current.Verdict != nil && !current.Verdict.Grade.Decided() {
			return fmt.Errorf("%w: completed session has an undecided verdict", ErrContract)
		}
	}
	return nil
}

// stateRank orders the lifecycle so a regression is a comparison rather than a
// table of forbidden pairs. StateDone and StateFailed share a rank because
// neither may follow the other: that pair is caught by the terminal check.
func stateRank(state State) int {
	switch state {
	case StatePending:
		return 1
	case StateRunning:
		return 2
	case StateDone, StateFailed:
		return 3
	default:
		return 0
	}
}
