package managed

// The two ordered enums are hand-written rather than generated. State and Grade
// are string values on the wire, but in Go they carry an order — a lifecycle
// that only moves forwards, and a judgment that is either decided or not — and
// the marshaling below is what keeps the wire form independent of the numbering
// the order needs. Every other enum in the contract is a plain string type and
// is generated from the schema.

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
// GradePending is a judgment not yet made, so neither may be read as a pass.
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

// Decided reports whether the evaluator has reached a final judgment;
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
