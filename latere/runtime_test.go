// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"context"
	"errors"
	"sync"
	"testing"

	managed "latere.ai/x/managed-agents"
	"latere.ai/x/managed-agents/contracttest"
)

// TestTheRuntimePassesTheContract runs the conformance suite over the fake
// platform. A restart is a new Runtime over the same platform, holding none
// of the first one's caches, which is what a new process would be.
func TestTheRuntimePassesTheContract(t *testing.T) {
	var mu sync.Mutex
	platforms := map[managed.Runtime]*fakePlatform{}
	contracttest.Suite{
		New: func() managed.Runtime {
			f := newFakePlatform(t)
			r := f.runtime(t)
			mu.Lock()
			defer mu.Unlock()
			platforms[r] = f
			return r
		},
		Restart: func(previous managed.Runtime) managed.Runtime {
			mu.Lock()
			defer mu.Unlock()
			return platforms[previous].runtime(t)
		},
		Rejecting: func() managed.Runtime {
			f := newFakePlatform(t)
			f.verdicts = []string{failed}
			return f.runtime(t)
		},
	}.Run(t)
}

func gradedSpec() managed.SessionSpec {
	return managed.SessionSpec{
		Model:        managed.ModelSelector{Preference: "balanced"},
		Instructions: "Follow the method.",
		Task:         "Reproduce the result.",
		Sandbox: managed.SandboxSpec{
			Packages: []managed.Package{{Manager: "pip", Name: "pypdf"}, {Manager: "pip", Name: "requests"}},
			Network:  managed.NetworkPolicy{Mode: managed.NetworkNone},
		},
		Evaluation: &managed.EvaluationSpec{Rubric: "1. The result matches.", MaxRevisions: 1},
	}
}

// run starts a session and polls it to the end, the way the controller does,
// carrying each checkpointed reference into the next poll.
func run(t *testing.T, r *Runtime, spec managed.SessionSpec) (managed.SessionRef, managed.Status) {
	t.Helper()
	ref, err := r.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	for range 20 {
		status, err := r.Poll(context.Background(), ref)
		if err != nil {
			t.Fatalf("Poll: %v", err)
		}
		ref = status.Ref
		if status.State.Terminal() {
			return ref, status
		}
	}
	t.Fatal("the session never ended")
	return ref, managed.Status{}
}

func TestARevisionFollowsANeedsRevisionGrade(t *testing.T) {
	f := newFakePlatform(t)
	f.verdicts = []string{needsRevision, passed}
	_, status := run(t, f.runtime(t), gradedSpec())
	if status.State != managed.StateDone || status.Verdict == nil || !status.Verdict.Passed() {
		t.Fatalf("status = %+v", status)
	}
	if f.graded != 2 {
		t.Fatalf("graded %d times, want 2", f.graded)
	}
}

func TestASpentRevisionBudgetEndsExhausted(t *testing.T) {
	f := newFakePlatform(t)
	f.verdicts = []string{needsRevision}
	spec := gradedSpec()
	spec.Evaluation.MaxRevisions = 0
	_, status := run(t, f.runtime(t), spec)
	if status.State != managed.StateFailed || status.TerminalReason != managed.ReasonEvaluationFailed {
		t.Fatalf("status = %+v", status)
	}
	if status.Verdict == nil || status.Verdict.Grade != managed.GradeExhausted {
		t.Fatalf("verdict = %+v", status.Verdict)
	}
	if f.graded != 1 {
		t.Fatalf("graded %d times, want 1: a zero budget grades once and never revises", f.graded)
	}
}

func TestAGraderStartedWrongIsRetriedThenFails(t *testing.T) {
	cases := map[string]func(string) (string, string, []string){
		"another agent": func(task string) (string, string, []string) { return "helper", task, graderTools },
		"another task":  func(string) (string, string, []string) { return graderName, "Grade it kindly.", graderTools },
		"a write tool":  func(task string) (string, string, []string) { return graderName, task, []string{"read", "write"} },
		"no subagent":   func(task string) (string, string, []string) { return "", task, nil },
	}
	for name, start := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakePlatform(t)
			f.start = start
			_, status := run(t, f.runtime(t), gradedSpec())
			if status.State != managed.StateFailed || status.TerminalReason != managed.ReasonRuntimeFailed || status.Verdict != nil {
				t.Fatalf("status = %+v", status)
			}
			if got := f.count("POST /v1/agents/sessions/{id}/events"); got != 1+graderAttempts {
				t.Fatalf("sent %d messages, want the task and %d grading requests", got, graderAttempts)
			}
		})
	}
}

func TestAGraderWithNoVerdictFailsTheSession(t *testing.T) {
	f := newFakePlatform(t)
	f.verdicts = []string{"It looks fine to me."}
	_, status := run(t, f.runtime(t), gradedSpec())
	if status.State != managed.StateFailed || status.Error == "" || status.Verdict != nil {
		t.Fatalf("status = %+v", status)
	}
}

func TestAPollReplayedAfterACrashSendsNothingTwice(t *testing.T) {
	f := newFakePlatform(t)
	r := f.runtime(t)
	ref, err := r.Start(context.Background(), gradedSpec())
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.Poll(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	// The process dies before the controller checkpoints first.Ref, and a
	// fresh one polls the reference it had.
	again, err := f.runtime(t).Poll(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if string(again.Ref.Locator) != string(first.Ref.Locator) {
		t.Fatalf("a replayed poll moved differently:\n%s\n%s", first.Ref.Locator, again.Ref.Locator)
	}
	if f.graded != 1 {
		t.Fatalf("graded %d times, want 1", f.graded)
	}
}

func TestStartRefusesWhatThePlatformCannotDo(t *testing.T) {
	cases := map[string]func(*managed.SessionSpec){
		"a package the image lacks": func(s *managed.SessionSpec) { s.Sandbox.Packages = []managed.Package{{Manager: "pip", Name: "torch"}} },
		"a pinned version": func(s *managed.SessionSpec) {
			s.Sandbox.Packages = []managed.Package{{Manager: "pip", Name: "numpy", Version: "1.0"}}
		},
		"bootstrap": func(s *managed.SessionSpec) { s.Sandbox.Bootstrap = [][]string{{"make"}} },
		"an allowlist": func(s *managed.SessionSpec) {
			s.Sandbox.Network = managed.NetworkPolicy{Mode: managed.NetworkAllowlist, AllowedDomains: []string{"example.org"}}
		},
		"an attachment": func(s *managed.SessionSpec) {
			s.Resources = []managed.Resource{{Kind: managed.ResourceAttachment, Attachment: &managed.AttachmentResource{Path: "a.txt", Content: []byte("a"), SHA256: "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"}}}
		},
		"an input": func(s *managed.SessionSpec) {
			s.Resources = []managed.Resource{{Kind: managed.ResourceInput, Input: &managed.InputResource{Path: "a.txt", Content: []byte("a"), SHA256: "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"}}}
		},
		"a repository": func(s *managed.SessionSpec) {
			s.Resources = []managed.Resource{{Kind: managed.ResourceRepository, Repository: &managed.RepositoryResource{URL: "https://github.com/o/r", Destination: "repo", Access: managed.AccessReadWrite, CredentialRef: "forge"}}}
		},
		"a findings file": func(s *managed.SessionSpec) { s.Evaluation.Findings = "verdict.json" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakePlatform(t)
			spec := gradedSpec()
			edit(&spec)
			if _, err := f.runtime(t).Start(context.Background(), spec); !errors.Is(err, managed.ErrUnsupported) {
				t.Fatalf("Start = %v, want unsupported", err)
			}
			if f.count("PUT /v1/agents/agents/{id}") != 0 || f.count("POST /v1/agents/sessions") != 0 {
				t.Fatal("something was written on the platform before the refusal")
			}
		})
	}
}

func TestStartRefusesANetworkThePreferenceDoesNotGive(t *testing.T) {
	f := newFakePlatform(t)
	f.preference = "listed"
	_, err := f.runtime(t).Start(context.Background(), gradedSpec())
	var unsupported *managed.UnsupportedError
	if !errors.As(err, &unsupported) || unsupported.Field != "sandbox.network.mode" {
		t.Fatalf("Start = %v", err)
	}
	if f.count("POST /v1/agents/sessions") != 0 {
		t.Fatal("a session was created")
	}
}

func TestAFailedTaskSendDeletesTheSession(t *testing.T) {
	f := newFakePlatform(t)
	f.fail["POST /v1/agents/sessions/ses_0001/events"] = 400
	if _, err := f.runtime(t).Start(context.Background(), gradedSpec()); err == nil {
		t.Fatal("Start succeeded")
	}
	if !f.sessions["ses_0001"].deleted {
		t.Fatal("the session was left behind")
	}
}

func TestCapabilitiesDeclareThePreferencesNetwork(t *testing.T) {
	for preference, want := range map[string]managed.NetworkMode{"open": managed.NetworkOpen, "none": managed.NetworkNone, "listed": ""} {
		f := newFakePlatform(t)
		f.preference = preference
		r := f.runtime(t)
		capabilities, err := r.Capabilities(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if want == "" && len(capabilities.NetworkModes) != 0 || want != "" && (len(capabilities.NetworkModes) != 1 || !capabilities.NetworkModes[want]) {
			t.Fatalf("%s: network modes = %v", preference, capabilities.NetworkModes)
		}
		if capabilities.OutputDir != OutputDir || !capabilities.Evaluation || capabilities.FindingsRounds || capabilities.InputDir != "" {
			t.Fatalf("capabilities = %+v", capabilities)
		}
	}
}
