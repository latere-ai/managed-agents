// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"encoding/json"
	"errors"
	"testing"
)

const testRuntime RuntimeName = "reference"

func testRef() SessionRef {
	return SessionRef{
		Runtime:        testRuntime,
		ID:             "session-1",
		Revision:       1,
		LocatorVersion: 1,
		Locator:        json.RawMessage(`{"shard":"a"}`),
	}
}

func testModel() ModelRef { return ModelRef{Provider: "reference", ID: "model-1"} }

func running(ref SessionRef) Status {
	return Status{Ref: ref, Model: testModel(), State: StateRunning, Usage: Usage{Model: testModel(), InputFresh: 10}}
}

func done(ref SessionRef) Status {
	status := running(ref)
	status.State = StateDone
	status.TerminalReason = ReasonSucceeded
	status.Usage.InputFresh = 20
	return status
}

// TestValidateObservationAcceptsALegalSequence is the floor: the rules must not
// reject a runtime that behaves.
func TestValidateObservationAcceptsALegalSequence(t *testing.T) {
	ref := testRef()
	first := running(ref)
	if err := ValidateObservation(ref, testRuntime, Status{}, first, GradingNotRequested); err != nil {
		t.Fatalf("first observation: %v", err)
	}
	if err := ValidateObservation(ref, testRuntime, first, done(ref), GradingNotRequested); err != nil {
		t.Fatalf("second observation: %v", err)
	}
}

// TestValidateObservationRejectsContractViolations covers every rule the type
// signatures cannot state. Each case is a runtime that compiles and passes its
// own happy-path test while being wrong.
func TestValidateObservationRejectsContractViolations(t *testing.T) {
	ref := testRef()

	advanced := ref
	advanced.Revision = 2
	advanced.Locator = json.RawMessage(`{"shard":"b"}`)

	sameRevisionNewLocator := ref
	sameRevisionNewLocator.Locator = json.RawMessage(`{"shard":"b"}`)

	sameRevisionNewLocatorVersion := ref
	sameRevisionNewLocatorVersion.LocatorVersion = 2

	otherSession := ref
	otherSession.ID = "session-2"

	usageWentDown := done(ref)
	usageWentDown.Usage.InputFresh = 1

	meterWentDown := running(ref)
	meterWentDown.Usage.Extra = map[Meter]int64{"container_seconds": 1}
	meterWentUp := meterWentDown
	meterWentUp.Usage.Extra = map[Meter]int64{"container_seconds": 5}

	otherModel := done(ref)
	otherModel.Model = ModelRef{Provider: "reference", ID: "model-2"}
	otherModel.Usage.Model = otherModel.Model

	noModel := running(ref)
	noModel.Model = ModelRef{}

	badState := running(ref)
	badState.State = StateUnknown

	gradedUndecided := done(ref)
	gradedUndecided.Verdict = &Verdict{Grade: GradePending}

	ungradedWithVerdict := done(ref)
	ungradedWithVerdict.Verdict = &Verdict{Grade: GradePassed}

	wrongRuntime := running(ref)
	wrongRuntime.Ref.Runtime = "someone-else"

	unvalidatableRef := running(ref)
	unvalidatableRef.Ref.Revision = 0

	cases := []struct {
		name     string
		expected SessionRef
		previous Status
		current  Status
		grading  Grading
	}{
		{"reference names another runtime", ref, Status{}, wrongRuntime, GradingUnknown},
		{"reference is malformed", ref, Status{}, unvalidatableRef, GradingUnknown},
		{"observation names another session", ref, Status{}, running(otherSession), GradingUnknown},
		{"revision went backwards", advanced, Status{}, running(ref), GradingUnknown},
		{"locator changed without a revision", ref, Status{}, running(sameRevisionNewLocator), GradingUnknown},
		{"locator version changed without a revision", ref, Status{}, running(sameRevisionNewLocatorVersion), GradingUnknown},
		{"state is not a defined state", ref, Status{}, badState, GradingUnknown},
		{"no resolved model", ref, Status{}, noModel, GradingUnknown},
		{"state regressed", ref, running(ref), Status{Ref: ref, Model: testModel(), State: StatePending}, GradingUnknown},
		{"terminal state changed", ref, done(ref), Status{Ref: ref, Model: testModel(), State: StateFailed, Usage: done(ref).Usage}, GradingUnknown},
		{"usage regressed", ref, running(ref), usageWentDown, GradingUnknown},
		{"a meter regressed", ref, meterWentUp, meterWentDown, GradingUnknown},
		{"resolved model changed", ref, running(ref), otherModel, GradingUnknown},
		{"graded session finished undecided", ref, running(ref), gradedUndecided, GradingRequested},
		{"graded session finished with no verdict", ref, running(ref), done(ref), GradingRequested},
		{"ungraded session finished with a verdict", ref, running(ref), ungradedWithVerdict, GradingNotRequested},
		{"unknown grading, undecided verdict", ref, running(ref), gradedUndecided, GradingUnknown},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateObservation(testCase.expected, testRuntime, testCase.previous, testCase.current, testCase.grading)
			if !errors.Is(err, ErrContract) {
				t.Fatalf("got %v, want an ErrContract", err)
			}
		})
	}
}

// TestValidateObservationGradingUnknownTolerates covers the third grading
// state: a session observed through a persisted reference records nothing about
// whether grading was requested, so neither a decided verdict nor its absence
// is a violation.
func TestValidateObservationGradingUnknownTolerates(t *testing.T) {
	ref := testRef()
	withVerdict := done(ref)
	withVerdict.Verdict = &Verdict{Grade: GradePassed}
	for _, status := range []Status{done(ref), withVerdict} {
		if err := ValidateObservation(ref, testRuntime, running(ref), status, GradingUnknown); err != nil {
			t.Fatalf("GradingUnknown rejected %+v: %v", status.Verdict, err)
		}
	}
}

// TestValidateObservationAllowsACheckpoint asserts the one thing that is
// allowed to move between observations: a raised revision carrying a new
// locator.
func TestValidateObservationAllowsACheckpoint(t *testing.T) {
	ref := testRef()
	advanced := ref
	advanced.Revision = 2
	advanced.Locator = json.RawMessage(`{"shard":"b"}`)
	if err := ValidateObservation(ref, testRuntime, running(ref), running(advanced), GradingUnknown); err != nil {
		t.Fatalf("checkpoint rejected: %v", err)
	}
}

// TestSessionRefRuntimeNameMatchesTheSchema keeps the Go and JSON halves of the
// contract agreeing about what a runtime may be called. Canonical form alone
// was the weaker test: a name with a space is its own lower-cased, trimmed
// self, so Go used to accept names the schema's pattern rejects.
func TestSessionRefRuntimeNameMatchesTheSchema(t *testing.T) {
	accepted := []RuntimeName{"reference", "anthropic-managed", "vendor.eu", "r2d2", "under_score"}
	refused := []RuntimeName{"", "Reference", "my runtime", " reference", "-leading", "trailing "}
	for _, name := range accepted {
		ref := testRef()
		ref.Runtime = name
		if err := ref.Validate(0); err != nil {
			t.Errorf("Validate rejected %q: %v", name, err)
		}
	}
	for _, name := range refused {
		ref := testRef()
		ref.Runtime = name
		if err := ref.Validate(0); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("Validate accepted %q (err = %v)", name, err)
		}
	}
}
