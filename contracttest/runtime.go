// Package contracttest is the black-box suite every runtime adapter has to
// pass.
//
// The port's rules are mostly about sequences of observations, not about
// shapes, so an adapter that ignores them still compiles and still passes a
// happy-path test of its own. This suite is what turns those rules into a
// failure an adapter author sees. Point it at a factory for the adapter and
// call [Suite.Run]:
//
//	func TestConformance(t *testing.T) {
//	    contracttest.Run(t, func() managed.Runtime { return myadapter.New() })
//	}
//
// The suite reaches nothing outside the runtime it is given. An adapter that
// needs a vendor account should skip the test when the credential is absent
// rather than fail it.
package contracttest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"testing"

	managed "latere.ai/x/managed-agents"
)

// Factory builds one runtime under test. It is called afresh for each case, so
// no state leaks from one case into the next.
type Factory func() managed.Runtime

// Suite is the conformance suite and the seams an adapter can supply.
type Suite struct {
	// New builds a fresh runtime holding no sessions. Required.
	New Factory
	// Restart models a process restart: it returns a runtime backed by the same
	// remote or on-disk state as the one it is given, as a new process would
	// see it. It is what makes the resumability case runnable — a runtime
	// declaring Capabilities.Resumable must find the session again through it,
	// and one declaring false must report ErrNotFound.
	//
	// Leaving it nil skips that case. An adapter declaring Resumable true and
	// supplying no Restart has not been tested on the claim it makes.
	Restart func(managed.Runtime) managed.Runtime
}

// Run is the suite for an adapter with nothing to configure.
func Run(t *testing.T, factory Factory) {
	t.Helper()
	Suite{New: factory}.Run(t)
}

// Run executes every case against the runtime the suite builds.
func (s Suite) Run(t *testing.T) {
	t.Helper()
	if s.New == nil {
		t.Fatal("contracttest: Suite.New is required")
	}

	t.Run("ungraded lifecycle", func(t *testing.T) {
		runtime := s.New()
		ref := start(t, runtime, spec(false))
		status := pollTerminal(t, runtime, ref, managed.GradingNotRequested)
		if status.State != managed.StateDone || status.Verdict != nil {
			t.Fatalf("ungraded terminal status = %+v", status)
		}
		if _, err := runtime.Events(context.Background(), ref); err != nil {
			t.Fatalf("Events before release: %v", err)
		}
		if err := runtime.Release(context.Background(), ref); err != nil {
			t.Fatalf("Release: %v", err)
		}
		if err := runtime.Release(context.Background(), ref); err != nil {
			t.Fatalf("repeated Release: %v", err)
		}
		for name, read := range map[string]func() error{
			"Events":        func() error { _, err := runtime.Events(context.Background(), ref); return err },
			"ListArtifacts": func() error { _, err := runtime.ListArtifacts(context.Background(), ref); return err },
			"FetchArtifact": func() error {
				_, err := runtime.FetchArtifact(context.Background(), ref, managed.Artifact{ID: "any", Path: "any"})
				return err
			},
		} {
			if err := read(); !errors.Is(err, managed.ErrNotFound) {
				t.Errorf("%s after release = %v, want ErrNotFound", name, err)
			}
		}
	})

	t.Run("artifacts are listed and fetchable", func(t *testing.T) {
		runtime := s.New()
		if !supports(t, runtime).Artifacts {
			t.Skip("runtime does not enumerate artifacts")
		}
		ref := start(t, runtime, spec(false))
		pollTerminal(t, runtime, ref, managed.GradingNotRequested)
		artifacts, err := runtime.ListArtifacts(context.Background(), ref)
		if err != nil {
			t.Fatalf("ListArtifacts: %v", err)
		}
		// An adapter is free to produce nothing. What it may not do is list a
		// file it then cannot hand over, or hand over a path it never listed:
		// the list is the caller's only way to name what to fetch.
		seen := make(map[string]bool, len(artifacts))
		for _, artifact := range artifacts {
			if artifact.Path == "" {
				t.Errorf("artifact %+v has no path", artifact)
			}
			if seen[artifact.Path] {
				t.Errorf("artifact path %q listed twice", artifact.Path)
			}
			seen[artifact.Path] = true
			if _, err := runtime.FetchArtifact(context.Background(), ref, artifact); err != nil {
				t.Errorf("FetchArtifact(%+v): %v", artifact, err)
			}
		}
		unlisted := managed.Artifact{ID: "not-listed", Path: "not/listed"}
		if _, err := runtime.FetchArtifact(context.Background(), ref, unlisted); !errors.Is(err, managed.ErrNotFound) {
			t.Errorf("FetchArtifact of an unlisted file = %v, want ErrNotFound", err)
		}
	})

	t.Run("artifacts are refused when unsupported", func(t *testing.T) {
		runtime := s.New()
		if supports(t, runtime).Artifacts {
			t.Skip("runtime enumerates artifacts")
		}
		ref := start(t, runtime, spec(false))
		pollTerminal(t, runtime, ref, managed.GradingNotRequested)
		// Refusing is the contract. An empty list would read as "the session
		// produced nothing", which is a different and wrong answer.
		if _, err := runtime.ListArtifacts(context.Background(), ref); !errors.Is(err, managed.ErrUnsupported) {
			t.Fatalf("ListArtifacts = %v, want ErrUnsupported", err)
		}
	})

	t.Run("cancel is refused when unsupported", func(t *testing.T) {
		runtime := s.New()
		if supports(t, runtime).Cancellation {
			t.Skip("runtime can cancel")
		}
		ref := start(t, runtime, spec(false))
		if err := runtime.Cancel(context.Background(), ref); !errors.Is(err, managed.ErrUnsupported) {
			t.Fatalf("Cancel = %v, want ErrUnsupported", err)
		}
	})

	t.Run("graded lifecycle", func(t *testing.T) {
		runtime := s.New()
		if !supports(t, runtime).Evaluation {
			t.Skip("runtime does not grade")
		}
		status := pollTerminal(t, runtime, start(t, runtime, spec(true)), managed.GradingRequested)
		if status.Verdict == nil || !status.Verdict.Grade.Decided() {
			t.Fatalf("graded terminal status = %+v", status)
		}
	})

	t.Run("evaluation is refused when unsupported", func(t *testing.T) {
		runtime := s.New()
		if supports(t, runtime).Evaluation {
			t.Skip("runtime grades")
		}
		if err := runtime.Validate(spec(true)); !errors.Is(err, managed.ErrUnsupported) {
			t.Fatalf("Validate(graded) = %v, want ErrUnsupported", err)
		}
	})

	t.Run("inputs match the declaration", func(t *testing.T) {
		runtime := s.New()
		capabilities := supports(t, runtime)
		withInput := inputSpec()
		if capabilities.InputDir == "" {
			// No input directory is a refusal, never a quiet copy into the
			// workspace, where the agent could change the file or commit it.
			if err := runtime.Validate(withInput); !errors.Is(err, managed.ErrUnsupported) {
				t.Fatalf("Validate(input) without an input directory = %v, want ErrUnsupported", err)
			}
			return
		}
		if !path.IsAbs(capabilities.InputDir) {
			t.Fatalf("InputDir %q is not an absolute sandbox path", capabilities.InputDir)
		}
		ref := start(t, runtime, withInput)
		pollTerminal(t, runtime, ref, managed.GradingNotRequested)
		if !capabilities.Artifacts {
			return
		}
		artifacts, err := runtime.ListArtifacts(context.Background(), ref)
		if err != nil {
			t.Fatalf("ListArtifacts: %v", err)
		}
		for _, artifact := range artifacts {
			if path.Base(artifact.Path) == inputName {
				t.Errorf("input listed as an artifact the session produced: %+v", artifact)
			}
		}
	})

	t.Run("findings rounds match the declaration", func(t *testing.T) {
		runtime := s.New()
		capabilities := supports(t, runtime)
		if !capabilities.Evaluation {
			t.Skip("runtime does not grade")
		}
		withFindings := spec(true)
		withFindings.Evaluation.Findings = "findings.json"
		if !capabilities.FindingsRounds {
			if err := runtime.Validate(withFindings); !errors.Is(err, managed.ErrUnsupported) {
				t.Fatalf("Validate(findings) without findings rounds = %v, want ErrUnsupported", err)
			}
			return
		}
		status := pollTerminal(t, runtime, start(t, runtime, withFindings), managed.GradingRequested)
		if status.Verdict == nil || !status.Verdict.Grade.Decided() {
			t.Fatalf("graded terminal status with findings = %+v", status)
		}
	})

	t.Run("cancel retains evidence", func(t *testing.T) {
		runtime := s.New()
		if !supports(t, runtime).Cancellation {
			t.Skip("runtime cannot cancel")
		}
		ref := start(t, runtime, spec(false))
		if err := runtime.Cancel(context.Background(), ref); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		if err := runtime.Cancel(context.Background(), ref); err != nil {
			t.Fatalf("repeated Cancel: %v", err)
		}
		status, err := runtime.Poll(context.Background(), ref)
		if err != nil || status.State != managed.StateFailed || status.TerminalReason != managed.ReasonCancelled {
			t.Fatalf("Poll after cancel = %+v, %v", status, err)
		}
		if _, err := runtime.Events(context.Background(), ref); err != nil {
			t.Fatalf("Events after cancel: %v", err)
		}
	})

	t.Run("resumability matches the declaration", func(t *testing.T) {
		if s.Restart == nil {
			t.Skip("suite supplies no Restart")
		}
		runtime := s.New()
		ref := start(t, runtime, spec(false))
		resumable := supports(t, runtime).Resumable
		// Through the persisted form, not the value still in hand. The claim
		// Resumable makes is about a reference that went to storage and came
		// back, so a locator that does not survive a round trip — or a lost
		// LocatorVersion — is exactly what this case is looking for.
		encoded, err := json.Marshal(ref)
		if err != nil {
			t.Fatalf("marshal ref: %v", err)
		}
		var restored managed.SessionRef
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Fatalf("unmarshal ref: %v", err)
		}
		restarted := s.Restart(runtime)
		_, err = restarted.Poll(context.Background(), restored)
		switch {
		case resumable && err != nil:
			t.Fatalf("Resumable runtime lost the session across a restart: %v", err)
		case !resumable && !errors.Is(err, managed.ErrNotFound):
			// Reporting a status for a session a restart cannot really reach is
			// the failure this case exists for: it is invented, and a caller
			// would act on it.
			t.Fatalf("non-resumable runtime answered a restarted Poll = %v, want ErrNotFound", err)
		}
	})

	t.Run("runtime mismatch", func(t *testing.T) {
		runtime := s.New()
		ref := start(t, runtime, spec(false))
		ref.Runtime = "another-runtime"
		calls := []func() error{
			func() error { _, err := runtime.Poll(context.Background(), ref); return err },
			func() error { _, err := runtime.Events(context.Background(), ref); return err },
			func() error { _, err := runtime.ListArtifacts(context.Background(), ref); return err },
			func() error { return runtime.Cancel(context.Background(), ref) },
			func() error { return runtime.Release(context.Background(), ref) },
		}
		for index, call := range calls {
			if err := call(); !errors.Is(err, managed.ErrRuntimeMismatch) {
				t.Errorf("call %d = %v", index, err)
			}
		}
	})
}

func supports(t *testing.T, runtime managed.Runtime) managed.Capabilities {
	t.Helper()
	capabilities, err := runtime.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	return capabilities
}

func spec(graded bool) managed.SessionSpec {
	spec := managed.SessionSpec{
		Model:        managed.ModelSelector{Preference: "best"},
		Instructions: "You are a careful worker.",
		Task:         "Write the answer to the output directory.",
		Sandbox:      managed.SandboxSpec{Network: managed.NetworkPolicy{Mode: managed.NetworkNone}},
	}
	if graded {
		spec.Evaluation = &managed.EvaluationSpec{Rubric: "The answer is correct.", MaxRevisions: 1}
	}
	return spec
}

// inputName is the file the input case hands a session. Its name is unusual
// on purpose, so finding it among the artifacts can only mean the runtime
// copied the input into what the session produced.
const inputName = "contracttest-input.txt"

func inputSpec() managed.SessionSpec {
	spec := spec(false)
	content := []byte("read me, do not change me")
	sum := sha256.Sum256(content)
	spec.Resources = []managed.Resource{{Kind: managed.ResourceInput, Input: &managed.InputResource{
		Path: inputName, MediaType: "text/plain", Content: content, SHA256: hex.EncodeToString(sum[:]),
	}}}
	return spec
}

func start(t *testing.T, runtime managed.Runtime, spec managed.SessionSpec) managed.SessionRef {
	t.Helper()
	if err := runtime.Validate(spec); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	ref, err := runtime.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := ref.Validate(managed.DefaultMaxLocatorBytes); err != nil {
		t.Fatalf("invalid start ref: %v", err)
	}
	if ref.Runtime != runtime.Name() {
		t.Fatalf("start ref names %q, runtime is %q", ref.Runtime, runtime.Name())
	}
	return ref
}

// pollTerminal drives the session to a terminal observation, holding every step
// to ValidateObservation. That is the point of the loop: a runtime whose usage
// resets, whose state goes backwards, or whose model changes fails here rather
// than in an application months later.
func pollTerminal(t *testing.T, runtime managed.Runtime, ref managed.SessionRef, grading managed.Grading) managed.Status {
	t.Helper()
	var previous managed.Status
	for range 20 {
		status, err := runtime.Poll(context.Background(), ref)
		if err != nil {
			t.Fatalf("Poll: %v", err)
		}
		if err := managed.ValidateObservation(ref, runtime.Name(), previous, status, grading); err != nil {
			t.Fatalf("observation: %v\nprevious=%+v\ncurrent=%+v", err, previous, status)
		}
		if status.Ref.Revision > ref.Revision {
			ref = status.Ref
		}
		previous = status
		if status.State.Terminal() {
			return status
		}
	}
	t.Fatal("session did not become terminal in 20 polls")
	return managed.Status{}
}
