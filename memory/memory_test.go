// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package memory

import (
	"context"
	"errors"
	"reflect"
	"testing"

	managed "latere.ai/x/managed-agents"
	"latere.ai/x/managed-agents/contracttest"
)

// TestRuntimeContract runs the conformance suite against the resumable
// runtime, declaring inputs and findings rounds as well. Restart rebuilds a
// Runtime around the same Store, which is what a process restart looks like to
// a backend that keeps its sessions somewhere outside the Runtime value.
func TestRuntimeContract(t *testing.T) {
	files := map[string][]byte{"outputs/answer.md": []byte("the answer"), "outputs/log.txt": []byte("...")}
	config := Config{Files: files, InputDir: InputDir, FindingsRounds: true}
	contracttest.Suite{
		// A fresh Store per case: the suite promises no state leaks between
		// them, and sharing one here would make the first case that enumerates
		// sessions fail on the order the cases happen to run in.
		New: func() managed.Runtime { return NewWithStore(nil, config) },
		Restart: func(previous managed.Runtime) managed.Runtime {
			return NewWithStore(previous.(*Runtime).store, config)
		},
		Rejecting: func() managed.Runtime {
			rejecting := config
			rejecting.Grade = managed.GradeFailed
			return NewWithStore(nil, rejecting)
		},
	}.Run(t)
}

// TestDefaultRuntimeContract runs the suite on the zero Config, which grades
// but declares neither inputs nor findings rounds: the refusal half of those
// two cases, on a runtime that would otherwise accept the specification.
func TestDefaultRuntimeContract(t *testing.T) {
	contracttest.Run(t, func() managed.Runtime { return New() })
}

// TestNonResumableRuntimeContract runs the same suite against a runtime that
// declares away every capability it can: it cannot survive a restart,
// enumerate artifacts, cancel, grade, take an input, or keep findings rounds.
// Every capability has to pass the suite in both declarations, or the flag is
// a comment rather than a contract.
func TestNonResumableRuntimeContract(t *testing.T) {
	config := Config{NonResumable: true, NoArtifacts: true, NoCancellation: true, NoEvaluation: true}
	contracttest.Suite{
		New: func() managed.Runtime { return NewWithStore(nil, config) },
		Restart: func(managed.Runtime) managed.Runtime {
			// A fresh process. Nothing it holds refers to the previous one,
			// which is the whole claim Resumable false makes.
			return NewWithStore(nil, config)
		},
	}.Run(t)
}

// TestNonResumableRuntimeLosesItsSessions is the direct statement of what the
// capability means, without the suite in the way.
func TestNonResumableRuntimeLosesItsSessions(t *testing.T) {
	store := NewStore()
	first := NewWithStore(store, Config{NonResumable: true})
	ref, err := first.Start(context.Background(), testSpec(false))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	capabilities, err := first.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if capabilities.Resumable {
		t.Fatal("NonResumable runtime declared Resumable")
	}
	// The Store was handed over and deliberately ignored, so a second Runtime
	// built the same way finds nothing.
	second := NewWithStore(store, Config{NonResumable: true})
	if _, err := second.Poll(context.Background(), ref); !errors.Is(err, managed.ErrNotFound) {
		t.Fatalf("Poll after restart = %v, want ErrNotFound", err)
	}
}

func TestRecordsSpecsAndResumesFromSharedStore(t *testing.T) {
	store := NewStore()
	first := NewWithStore(store, Config{PollsBeforeDone: 2})
	spec := testSpec(false)
	ref, err := first.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	second := NewWithStore(store, Config{PollsBeforeDone: 2})
	status, err := second.Poll(context.Background(), ref)
	if err != nil || status.State != managed.StateRunning {
		t.Fatalf("resumed Poll = %+v, %v", status, err)
	}
	if got := second.Specs(); len(got) != 1 || !reflect.DeepEqual(got[0], spec) {
		t.Fatalf("Specs = %+v", got)
	}
}

func TestArtifactsAreSortedCopiedAndStrictlyFetched(t *testing.T) {
	runtime := NewWithStore(nil, Config{Files: map[string][]byte{"b.txt": []byte("b"), "a.txt": []byte("a")}})
	ref, _ := runtime.Start(context.Background(), testSpec(false))
	artifacts, err := runtime.ListArtifacts(context.Background(), ref)
	if err != nil || len(artifacts) != 2 || artifacts[0].Path != "a.txt" {
		t.Fatalf("ListArtifacts = %+v, %v", artifacts, err)
	}
	body, err := runtime.FetchArtifact(context.Background(), ref, artifacts[0])
	if err != nil || string(body) != "a" {
		t.Fatalf("FetchArtifact = %q, %v", body, err)
	}
	body[0] = 'x'
	again, _ := runtime.FetchArtifact(context.Background(), ref, artifacts[0])
	if string(again) != "a" {
		t.Fatalf("artifact storage was mutated: %q", again)
	}
	for _, artifact := range []managed.Artifact{{ID: "artifact-1", Path: "../a.txt"}, {ID: "unknown", Path: "a.txt"}} {
		if _, err := runtime.FetchArtifact(context.Background(), ref, artifact); !errors.Is(err, managed.ErrNotFound) {
			t.Fatalf("FetchArtifact(%+v) = %v", artifact, err)
		}
	}
}

func TestCapabilitiesAndExactModelValidation(t *testing.T) {
	runtime := New()
	capabilities, err := runtime.Capabilities(context.Background())
	if err != nil || capabilities.OutputDir != OutputDir || !capabilities.Evaluation {
		t.Fatalf("Capabilities = %+v, %v", capabilities, err)
	}
	models, err := runtime.Models(context.Background())
	if err != nil || len(models) != 1 || models[0].Ref.Provider != "memory" {
		t.Fatalf("Models = %+v, %v", models, err)
	}
	spec := testSpec(false)
	spec.Model = managed.ModelSelector{Exact: &managed.ModelRef{Provider: "other", ID: "same"}}
	if err := runtime.Validate(spec); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatalf("Validate exact model = %v", err)
	}
	spec = testSpec(false)
	spec.Sandbox.Packages = []managed.Package{{Manager: "npm", Name: "x"}}
	if err := runtime.Validate(spec); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatalf("Validate package = %v", err)
	}
	if len(runtime.Specs()) != 0 {
		t.Fatal("validation mutated the store")
	}
}

func TestConfiguredEvaluationFailureAndEvents(t *testing.T) {
	events := []managed.Event{{ID: "1", Role: managed.RoleAgent, Kind: managed.KindMessage, Content: "partial"}}
	runtime := NewWithStore(nil, Config{Grade: managed.GradeExhausted, Events: events})
	ref, _ := runtime.Start(context.Background(), testSpec(true))
	status, err := runtime.Poll(context.Background(), ref)
	if err != nil || status.State != managed.StateFailed || status.TerminalReason != managed.ReasonEvaluationFailed || status.Verdict.Grade != managed.GradeExhausted {
		t.Fatalf("Poll = %+v, %v", status, err)
	}
	got, _ := runtime.Events(context.Background(), ref)
	got[0].Content = "changed"
	again, _ := runtime.Events(context.Background(), ref)
	if again[0].Content != "partial" {
		t.Fatalf("events were mutated: %+v", again)
	}
}

func TestFailuresAndCancellationSurface(t *testing.T) {
	for _, point := range []FailurePoint{FailValidate, FailStart, FailPoll, FailEvents, FailList, FailFetch, FailCancel, FailRelease} {
		t.Run(string(point), func(t *testing.T) {
			injected := errors.New("injected")
			runtime := NewWithStore(nil, Config{Files: map[string][]byte{"a": []byte("a")}, Failures: map[FailurePoint]error{point: injected}})
			spec := testSpec(false)
			if point == FailValidate || point == FailStart {
				_, err := runtime.Start(context.Background(), spec)
				if !errors.Is(err, injected) {
					t.Fatalf("Start = %v", err)
				}
				return
			}
			delete(runtime.config.Failures, point)
			ref, _ := runtime.Start(context.Background(), spec)
			runtime.config.Failures[point] = injected
			var err error
			switch point {
			case FailPoll:
				_, err = runtime.Poll(context.Background(), ref)
			case FailEvents:
				_, err = runtime.Events(context.Background(), ref)
			case FailList:
				_, err = runtime.ListArtifacts(context.Background(), ref)
			case FailFetch:
				_, err = runtime.FetchArtifact(context.Background(), ref, managed.Artifact{ID: "artifact-1", Path: "a"})
			case FailCancel:
				err = runtime.Cancel(context.Background(), ref)
			case FailRelease:
				err = runtime.Release(context.Background(), ref)
			}
			if !errors.Is(err, injected) {
				t.Fatalf("operation = %v", err)
			}
		})
	}

	runtime := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runtime.Capabilities(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Capabilities cancellation = %v", err)
	}
	if _, err := runtime.Models(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Models cancellation = %v", err)
	}
}

func testSpec(graded bool) managed.SessionSpec {
	spec := managed.SessionSpec{
		Model:        managed.ModelSelector{Preference: "best"},
		Instructions: "instructions",
		Task:         "task",
		Sandbox:      managed.SandboxSpec{Network: managed.NetworkPolicy{Mode: managed.NetworkOpen}},
	}
	if graded {
		spec.Evaluation = &managed.EvaluationSpec{Rubric: "rubric", MaxRevisions: 2}
	}
	return spec
}
