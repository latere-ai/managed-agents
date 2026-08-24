package managed

import (
	"encoding/json"
	"fmt"
)

// State is the externally visible lifecycle of a managed session.
type State uint8

// The lifecycle a session moves through. StateUnknown is the zero value, so a
// Status that was never filled in cannot pass for a live or a finished session.
const (
	StateUnknown State = iota
	StatePending
	StateRunning
	StateDone
	StateFailed
)

var stateNames = map[State]string{
	StateUnknown: "unknown",
	StatePending: "pending",
	StateRunning: "running",
	StateDone:    "done",
	StateFailed:  "failed",
}

// Terminal reports whether no further state change is expected. It is the
// signal to stop polling and release the session.
func (s State) Terminal() bool { return s == StateDone || s == StateFailed }

// Valid reports whether s is a state this package defines. The test is an
// ordered range, so StateFailed must stay the last member of the block above.
func (s State) Valid() bool { return s > StateUnknown && s <= StateFailed }

// String returns the value's stable name.
func (s State) String() string {
	if name, ok := stateNames[s]; ok {
		return name
	}
	return fmt.Sprintf("State(%d)", s)
}

// MarshalJSON encodes the state as its stable name, so the wire form does not
// depend on the numbering of the constants.
func (s State) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// UnmarshalJSON decodes a stable name and rejects any it does not know, so a
// state this build cannot interpret fails loudly instead of arriving as
// StateUnknown.
func (s *State) UnmarshalJSON(raw []byte) error {
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		return err
	}
	for value, candidate := range stateNames {
		if candidate == name {
			*s = value
			return nil
		}
	}
	return fmt.Errorf("unknown managed state %q", name)
}

// Grade is a runtime evaluator's decision.
type Grade uint8

// The grades an evaluator can record. GradeUnknown is the zero value and
// GradePending is a judgement not yet made, so neither may be read as a pass.
// GradeNeedsRevision leaves the revision budget open where GradeExhausted
// means it is spent, and GradeFailed is a decided rejection of the work.
const (
	GradeUnknown Grade = iota
	GradePending
	GradePassed
	GradeNeedsRevision
	GradeExhausted
	GradeFailed
)

var gradeNames = map[Grade]string{
	GradeUnknown:       "unknown",
	GradePending:       "pending",
	GradePassed:        "passed",
	GradeNeedsRevision: "needs_revision",
	GradeExhausted:     "exhausted",
	GradeFailed:        "failed",
}

// Decided reports whether the evaluator has reached a final judgement;
// GradeUnknown and GradePending have not.
func (g Grade) Decided() bool {
	return g == GradePassed || g == GradeNeedsRevision || g == GradeExhausted || g == GradeFailed
}

// String returns the value's stable name.
func (g Grade) String() string {
	if name, ok := gradeNames[g]; ok {
		return name
	}
	return fmt.Sprintf("Grade(%d)", g)
}

// MarshalJSON encodes the grade as its stable name, so the wire form does not
// depend on the numbering of the constants.
func (g Grade) MarshalJSON() ([]byte, error) { return json.Marshal(g.String()) }

// UnmarshalJSON decodes a stable name and rejects any it does not know, so a
// grade this build cannot interpret fails loudly instead of arriving as
// GradeUnknown.
func (g *Grade) UnmarshalJSON(raw []byte) error {
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		return err
	}
	for value, candidate := range gradeNames {
		if candidate == name {
			*g = value
			return nil
		}
	}
	return fmt.Errorf("unknown managed grade %q", name)
}

// NetworkMode is the outbound network policy a sandbox runs under. The empty
// value is not a mode: validation reads an omitted mode as NetworkNone, so a
// spec that forgets to say gets the closed sandbox rather than the open one.
type NetworkMode string

// The outbound network policies. NetworkNone closes the sandbox off and
// NetworkOpen lifts every restriction; neither may carry domains or secrets.
// NetworkAllowlist is the only mode that may carry secrets, and each one is
// bound to a domain the allowlist names.
const (
	NetworkNone      NetworkMode = "none"
	NetworkAllowlist NetworkMode = "allowlist"
	NetworkOpen      NetworkMode = "open"
)

// AccessMode is how a session may use a mounted repository. Runtimes advertise
// the modes they honour in Capabilities, so a spec asking for more than the
// runtime allows is refused before the session starts.
type AccessMode string

// The repository access modes. AccessReadWrite also obliges the spec to name a
// credential reference, since a mount the session can push to needs one.
const (
	AccessReadOnly  AccessMode = "ro"
	AccessReadWrite AccessMode = "rw"
)

// ResourceKind says which payload of a Resource is the live one. Exactly one
// payload pointer may be set and it must be the one the kind names; validation
// rejects the mismatch rather than guessing.
type ResourceKind string

// The resource kinds. ResourceAttachment carries file content inline with a
// SHA-256 the validator recomputes; ResourceRepository names an HTTPS
// repository to place at a destination inside the workspace.
const (
	ResourceAttachment ResourceKind = "attachment"
	ResourceRepository ResourceKind = "repository"
)

// TerminalReason says why a session stopped, and is carried only on a terminal
// Status. StateFailed alone cannot tell a run the runtime broke on from one
// that was cancelled or timed out, and operators chase those in different
// places.
type TerminalReason string

// Why a session stopped. ReasonSucceeded and ReasonEvaluationFailed are
// outcomes of the work itself; ReasonCancelled, ReasonTimedOut and
// ReasonRuntimeFailed are an account of a run that never reached
// a verdict. ReasonPolicyRefused is for a session a runtime declined on policy
// grounds rather than ran, without the work ever starting.
const (
	ReasonSucceeded        TerminalReason = "succeeded"
	ReasonEvaluationFailed TerminalReason = "evaluation_failed"
	ReasonCancelled        TerminalReason = "cancelled"
	ReasonTimedOut         TerminalReason = "timed_out"
	ReasonRuntimeFailed    TerminalReason = "runtime_failed"
	ReasonPolicyRefused    TerminalReason = "policy_refused"
)

// Role is who a transcript event is attributed to. The set is closed: an
// adapter maps every runtime-native speaker onto one of these.
type Role string

// The event roles: the model, the caller it answers, and the harness around
// them both.
const (
	RoleAgent  Role = "agent"
	RoleUser   Role = "user"
	RoleSystem Role = "system"
)

// EventKind is what a transcript event is. Unlike Role the set is deliberately
// open: the kinds below are named so callers compare against a constant rather
// than a bare string, while a runtime-native kind passes through unchanged
// rather than being dropped or coerced.
type EventKind string

// The well-known event kinds. Others pass through as the runtime names them.
const (
	KindMessage    EventKind = "message"
	KindThinking   EventKind = "thinking"
	KindToolUse    EventKind = "tool_use"
	KindToolResult EventKind = "tool_result"
)
