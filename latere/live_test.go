// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	managed "latere.ai/x/managed-agents"
)

// TestRealLatereEndToEnd runs one small graded session on the live Latere
// platform, as the person whose credential LATERE_API_KEY or
// LATERE_TOKEN_COMMAND names, and charges their wallet a few cents. It is
// opt-in (MANAGED_AGENTS_LATERE_E2E=1), because it reaches the network and
// spends money; it prints the transcript when the session does not pass, before the
// session is released, so a failure says why.
func TestRealLatereEndToEnd(t *testing.T) {
	if os.Getenv("MANAGED_AGENTS_LATERE_E2E") != "1" {
		t.Skip("set MANAGED_AGENTS_LATERE_E2E=1 and a Latere credential to run a real session")
	}
	var tokens TokenSource
	switch {
	case os.Getenv("LATERE_API_KEY") != "":
		tokens = PersonalAccessToken("https://auth.latere.ai", os.Getenv("LATERE_API_KEY"), defaultHTTPClient())
	case os.Getenv("LATERE_TOKEN_COMMAND") != "":
		tokens = Command(os.Getenv("LATERE_TOKEN_COMMAND"))
	default:
		t.Fatal("set LATERE_API_KEY or LATERE_TOKEN_COMMAND")
	}
	model := os.Getenv("MANAGED_AGENTS_LATERE_MODEL")
	if model == "" {
		model = "balanced"
	}
	r, err := New(Config{Tokens: tokens})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if _, err := r.Capabilities(ctx); err != nil {
		t.Fatal(err)
	}
	selector := managed.ModelSelector{Preference: model}
	if strings.Contains(model, "/") {
		ref := modelRef(model)
		selector = managed.ModelSelector{Exact: &ref}
	}
	spec := managed.SessionSpec{
		Model:        selector,
		Instructions: "You are a careful research engineer. Work in the working directory.",
		Task:         "Create outputs/result.txt containing only the number that is the sum of 2 and 3. Then end your turn.",
		Sandbox:      managed.SandboxSpec{Network: managed.NetworkPolicy{Mode: managed.NetworkOpen}},
		Evaluation:   &managed.EvaluationSpec{Rubric: "1. outputs/result.txt exists.\n2. It contains the number 5 and nothing else.", MaxRevisions: 1},
	}
	ref, err := r.Start(ctx, spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		if err := r.Release(context.WithoutCancel(ctx), ref); err != nil {
			t.Errorf("Release: %v", err)
		}
	}()
	var status managed.Status
	for !status.State.Terminal() {
		if status, err = r.Poll(ctx, ref); err != nil {
			t.Fatalf("Poll: %v", err)
		}
		if !status.State.Terminal() {
			time.Sleep(5 * time.Second)
		}
	}
	artifacts, err := r.ListArtifacts(ctx, ref)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	t.Logf("session %s on %s/%s: %s %s %q, %d artifacts, %d micro-USD", ref.ID, status.Model.Provider, status.Model.ID,
		status.State, status.TerminalReason, status.Error, len(artifacts), status.Usage.Extra[CostMeter])
	if !status.Passed() {
		events, _ := r.Events(ctx, ref)
		for _, event := range events {
			if event.Kind == "model.request" {
				continue
			}
			t.Logf("%s %s %s %.400s", event.Kind, event.Role, event.Tool, event.Content)
		}
		t.Fatalf("the session did not pass: %+v", status.Verdict)
	}
}
