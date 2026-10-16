// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	managed "latere.ai/x/managed-agents"
)

func TestModelNamesReadAsProviderQualifiedRefs(t *testing.T) {
	cases := map[string]managed.ModelRef{
		"anthropic/claude-sonnet-4.6":    {Provider: "anthropic", ID: "claude-sonnet-4.6"},
		"free/google/gemma-4-26b-a4b-it": {Provider: "google", ID: "gemma-4-26b-a4b-it", BillingVariant: "free"},
		"tier/thorough":                  {Provider: "latere", ID: "tier/thorough"},
		"openrouter/auto/variant":        {Provider: "openrouter", ID: "auto/variant"},
	}
	for name, want := range cases {
		if got := modelRef(name); got != want {
			t.Errorf("modelRef(%q) = %+v, want %+v", name, got, want)
		}
		if back := modelName(want); back != name {
			t.Errorf("modelName(%+v) = %q, want %q", want, back, name)
		}
	}
}

func TestUsageIsNormalizedOncePerCall(t *testing.T) {
	cached, written, cost, search := int64(30), int64(5), int64(4100), int64(10000)
	payload := func(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
	events := normalize([]wireEvent{
		{ID: "e1", Seq: 1, Type: "model.request", Payload: payload(wireModelRequest{Model: "openai/gpt-6.1-sol", Cost: &cost,
			Usage: &wireUsage{InputTokens: 100, OutputTokens: 40, CacheRead: &cached, CacheWrite: &written, ReasoningTokens: 12}})},
		{ID: "e2", Seq: 2, Type: "model.request", Payload: payload(wireModelRequest{Model: "free/google/gemma", Outcome: "error"})},
		{ID: "e3", Seq: 3, Type: "tool.result", Payload: payload(wireToolResult{Content: []wireBlock{{Type: "text", Text: "found"}}, Cost: &search})},
	})
	first := events[0].Usage
	if first == nil || first.InputFresh != 100 || first.InputCached != 30 || first.CacheWrite != 5 || first.Output != 40 ||
		first.Details["reasoning"] != 12 || first.Extra[CostMeter] != 4100 || first.Model.Provider != "openai" {
		t.Fatalf("usage = %+v", first)
	}
	if events[1].Usage != nil {
		t.Fatalf("a call that reported nothing carries usage %+v", events[1].Usage)
	}
	model := managed.ModelRef{Provider: "openai", ID: "gpt-6.1-sol"}
	sum := total(events, model)
	if sum.InputFresh != 100 || sum.Output != 40 || sum.Extra[CostMeter] != 14100 || sum.Model != model {
		t.Fatalf("total = %+v", sum)
	}
	if !sum.MonotonicFrom(total(events[:1], model)) {
		t.Fatal("the total over a longer log is smaller")
	}
}

func TestTheTranscriptNamesSpeakersAndTheGrader(t *testing.T) {
	payload := func(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
	message := func(blocks ...wireBlock) wireAgentMessage {
		var m wireAgentMessage
		m.Message.Blocks = blocks
		return m
	}
	events := normalize([]wireEvent{
		{ID: "e1", Type: "user.message", Payload: payload(wireUserMessage{Content: []wireBlock{{Type: "text", Text: "Do it."}}})},
		{ID: "e2", Type: "agent.message", Payload: payload(message(wireBlock{Type: "thinking", Thinking: "first, read"}))},
		{ID: "e3", Type: "agent.message", Payload: payload(message(wireBlock{Type: "text", Text: "Done."}, wireBlock{Type: "text", Text: " "}))},
		{ID: "e4", Type: "agent.tool_use", Payload: payload(wireToolUse{Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)})},
		{ID: "e5", Type: "session.error", Payload: payload(wireSessionError{Code: "model_busy", Message: "busy"})},
		{ID: "e6", Type: "agent.message", Thread: "t1", Payload: payload(message(wireBlock{Type: "text", Text: "Verdict."}))},
		{ID: "e7", Type: "work.saved", Payload: payload(map[string]int{"covers": 3})},
	})
	want := []struct {
		role    managed.Role
		kind    managed.EventKind
		content string
	}{
		{managed.RoleUser, managed.KindMessage, "Do it."},
		{managed.RoleAgent, managed.KindThinking, "first, read"},
		{managed.RoleAgent, managed.KindMessage, "Done."},
		{managed.RoleAgent, managed.KindToolUse, `{"command":"ls"}`},
		{managed.RoleSystem, "session.error", "model_busy: busy"},
		{managed.RoleAgent, managed.KindMessage, "[grader] Verdict."},
		{managed.RoleSystem, "work.saved", ""},
	}
	for i, w := range want {
		got := events[i]
		if got.Role != w.role || got.Kind != w.kind || got.Content != w.content || got.ID == "" || len(got.Raw) == 0 {
			t.Errorf("event %d = %+v, want %+v", i, got, w)
		}
	}
	if events[3].Tool != "bash" {
		t.Errorf("tool = %q", events[3].Tool)
	}
}

func TestAVerdictIsTheLastGradedObject(t *testing.T) {
	model := managed.ModelRef{Provider: "anthropic", ID: "claude-sonnet-5.5"}
	verdict, err := parseVerdict(`An example {"grade": "x"} is not mine. `+failed+` trailing words`, model)
	if err != nil || verdict.Grade != managed.GradeFailed ||
		!strings.Contains(verdict.Explanation, "Failed criteria:\n  1. result: outputs/result.txt is wrong") ||
		!strings.HasSuffix(verdict.Explanation, "Graded by anthropic/claude-sonnet-5.5.") {
		t.Fatalf("verdict = %+v, %v", verdict, err)
	}
	for name, message := range map[string]string{
		"no object":   "It passes.",
		"a bad grade": `{"grade": "great", "criteria": [{"id": 1}]}`,
		"no criteria": `{"grade": "passed"}`,
	} {
		if _, err := parseVerdict(message, model); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestTheWorkerManifestIsNamedByItsContent(t *testing.T) {
	a, err := workerManifest(DefaultProduct, "Follow the method.", "base", "tier/thorough", "anthropic/claude-sonnet-5.5", "1. It matches.")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := workerManifest(DefaultProduct, "Follow the method.", "base", "tier/thorough", "anthropic/claude-sonnet-5.5", "1. It matches.")
	c, _ := workerManifest(DefaultProduct, "Follow the method.", "base", "tier/thorough", "anthropic/claude-sonnet-5.5", "1. It differs.")
	ungraded, _ := workerManifest(DefaultProduct, "Follow the method.", "base", "tier/thorough", "", "")
	if a.Metadata.Name != b.Metadata.Name || a.Metadata.Name == c.Metadata.Name || !strings.HasPrefix(a.Metadata.Name, DefaultProduct.Key+"-") {
		t.Fatalf("names %q %q %q", a.Metadata.Name, b.Metadata.Name, c.Metadata.Name)
	}
	if len(ungraded.Spec.Subagents) != 0 || len(a.Spec.Subagents) != 1 {
		t.Fatal("only a graded session's agent declares the grader")
	}
	grader := a.Spec.Subagents[0].Spec
	if grader.Model.Name != "anthropic/claude-sonnet-5.5" || !strings.Contains(grader.Instructions, "1. It matches.") ||
		strings.Join(grader.Tools, ",") != "read,grep,glob" {
		t.Fatalf("grader = %+v", grader)
	}
	if a.Spec.Approvals.Mode != "progressive" || len(a.Spec.Approvals.AlwaysAllow) != len(workerTools) || len(a.Spec.Permissions) == 0 {
		t.Fatalf("worker approvals and permissions = %+v %+v", a.Spec.Approvals, a.Spec.Permissions)
	}
}

func TestAGraderRunsOnAModelNotALevel(t *testing.T) {
	f := newFakePlatform(t)
	r := f.runtime(t)
	cases := []struct {
		selector    managed.ModelSelector
		model, grad string
	}{
		{managed.ModelSelector{Preference: "fast"}, "tier/quick", "google/gemma"},
		{managed.ModelSelector{Preference: "best"}, "tier/sustained", "moonshotai/kimi-k3"},
		{managed.ModelSelector{Exact: &managed.ModelRef{Provider: "openai", ID: "gpt-6.1-sol"}}, "openai/gpt-6.1-sol", "openai/gpt-6.1-sol"},
		{managed.ModelSelector{Exact: &managed.ModelRef{Provider: "google", ID: "gemma", BillingVariant: "free"}}, "free/google/gemma", "google/gemma"},
	}
	for _, c := range cases {
		spec := gradedSpec()
		spec.Model = c.selector
		model, grader, err := r.sessionModels(context.Background(), "bearer-1", spec)
		if err != nil || model != c.model || grader != c.grad {
			t.Errorf("%+v: %q %q %v", c.selector, model, grader, err)
		}
	}
	spec := gradedSpec()
	spec.Model = managed.ModelSelector{Exact: &managed.ModelRef{Provider: "latere", ID: "tier/free-only"}}
	if _, _, err := r.sessionModels(context.Background(), "bearer-1", spec); err == nil {
		t.Fatal("a level with only free models gave a grader model")
	}
}

func TestModelsListTheLevelsAndTheCatalog(t *testing.T) {
	f := newFakePlatform(t)
	r := f.runtime(t)
	models, err := r.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(models))
	for _, m := range models {
		names = append(names, modelName(m.Ref)+":"+strings.Join(m.Preferences, ","))
	}
	want := "tier/sustained:best tier/thorough:balanced tier/quick:fast anthropic/claude-sonnet-4.6: openai/gpt-6.1-sol:"
	if strings.Join(names, " ") != want {
		t.Fatalf("models = %s", strings.Join(names, " "))
	}
	if got := f.count("GET /v1/models/models"); got != 2 {
		t.Fatalf("catalog read %d times, want its 2 pages", got)
	}
}

func TestEffortsNameEachLevelsModelAndPrice(t *testing.T) {
	f := newFakePlatform(t)
	efforts, err := f.runtime(t).Efforts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(efforts) != 3 || efforts[0].Level != "sustained" || efforts[1].Level != "thorough" || efforts[2].Level != "quick" {
		t.Fatalf("efforts = %+v", efforts)
	}
	thorough := efforts[1]
	if thorough.Preference != "balanced" || thorough.Model != (managed.ModelRef{Provider: "anthropic", ID: "claude-sonnet-5.5"}) ||
		thorough.Input != 2_000_000 || thorough.Output != 10_000_000 || !thorough.Available {
		t.Fatalf("thorough = %+v", thorough)
	}
	if efforts[0].Model.ID != "kimi-k3" || efforts[2].Input != 0 {
		t.Fatalf("sustained and quick = %+v %+v", efforts[0], efforts[2])
	}
	for amount, want := range map[string]int64{"0.1": 100_000, "12.5": 12_500_000, "x": 0, "": 0, "3": 3_000_000} {
		if got := perMillion(amount, 1_000_000); got != want {
			t.Errorf("perMillion(%q) = %d, want %d", amount, got, want)
		}
	}
	if perMillion("1", 1000) != 1_000_000_000 || perMillion("1", 0) != 0 {
		t.Error("perMillion scales by per")
	}
}

func TestArtifactsComeFromTheSessionsFolder(t *testing.T) {
	f := newFakePlatform(t)
	// More files than one page of Storage's listing, sorted so the run's own
	// files come after the page break, as an installed library in the
	// working directory puts them.
	f.files = map[string]string{"outputs/result.txt": "5\n", "README.md": "# r\n", ".pylibs/a.py": "a", ".pylibs/b.py": "b", ".pylibs/c.py": "c"}
	r := f.runtime(t)
	ref, status := run(t, r, gradedSpec())
	if status.State != managed.StateDone {
		t.Fatalf("status = %+v", status)
	}
	artifacts, err := r.ListArtifacts(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, a := range artifacts {
		paths = append(paths, a.Path)
	}
	if strings.Join(paths, ",") != ".pylibs/a.py,.pylibs/b.py,.pylibs/c.py,README.md,outputs/result.txt" {
		t.Fatalf("artifacts = %v: inputs and compiled files are not what the session produced", paths)
	}
	body, err := r.FetchArtifact(context.Background(), ref, artifacts[4])
	if err != nil || string(body) != "5\n" {
		t.Fatalf("fetch = %q, %v", body, err)
	}
	for _, path := range []string{"attachments/evt_1/paper.pdf", "../other/secret", "missing.txt"} {
		if _, err := r.FetchArtifact(context.Background(), ref, managed.Artifact{ID: path, Path: path}); !errors.Is(err, managed.ErrNotFound) {
			t.Errorf("fetch %q = %v", path, err)
		}
	}
}

func TestASessionThatChangedNothingHasNoArtifacts(t *testing.T) {
	f := newFakePlatform(t)
	f.hold = true
	r := f.runtime(t)
	ref, err := r.Start(context.Background(), gradedSpec())
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := r.ListArtifacts(context.Background(), ref)
	if err != nil || len(artifacts) != 0 {
		t.Fatalf("artifacts = %v, %v", artifacts, err)
	}
}

func TestReleaseWaitsForAnInterruptedTurn(t *testing.T) {
	f := newFakePlatform(t)
	f.hold = true
	r := f.runtime(t)
	ref, err := r.Start(context.Background(), gradedSpec())
	if err != nil {
		t.Fatal(err)
	}
	if status, err := r.Poll(context.Background(), ref); err != nil || status.State != managed.StateRunning {
		t.Fatalf("a held turn polls %+v, %v", status, err)
	}
	if err := r.Release(context.Background(), ref); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !f.sessions[ref.ID].deleted || f.count("DELETE /v1/agents/sessions/{id}") != 2 {
		t.Fatalf("deleted=%v after %d deletes", f.sessions[ref.ID].deleted, f.count("DELETE /v1/agents/sessions/{id}"))
	}
	if err := r.Cancel(context.Background(), ref); err != nil {
		t.Fatalf("Cancel after release: %v", err)
	}
}

func TestATurnThatNeverStopsIsReportedNotDeleted(t *testing.T) {
	f := newFakePlatform(t)
	f.hold = true
	r := f.runtime(t)
	ref, err := r.Start(context.Background(), gradedSpec())
	if err != nil {
		t.Fatal(err)
	}
	// The platform refuses every delete, as it does a session whose turn
	// never stops.
	f.mu.Lock()
	f.sessions[ref.ID].session.Status = "running"
	f.mu.Unlock()
	interrupts := 0
	f.spawn = nil
	r.sleep = func(context.Context, time.Duration) error {
		f.mu.Lock()
		f.sessions[ref.ID].session.Status = "running"
		f.mu.Unlock()
		interrupts++
		return nil
	}
	if err := r.Release(context.Background(), ref); err == nil {
		t.Fatal("Release reported success for a session it could not delete")
	}
	if interrupts != releaseAttempts {
		t.Fatalf("waited %d times", interrupts)
	}
}

func TestAStoppedSessionReportsWhy(t *testing.T) {
	for stop, want := range map[string]managed.TerminalReason{
		"budget": managed.ReasonRuntimeFailed, "turn_limit": managed.ReasonRuntimeFailed,
		"tool_confirmation": managed.ReasonRuntimeFailed, "interrupted": managed.ReasonCancelled,
	} {
		f := newFakePlatform(t)
		f.stop = stop
		_, status := run(t, f.runtime(t), gradedSpec())
		if status.State != managed.StateFailed || status.TerminalReason != want {
			t.Errorf("%s: status = %+v", stop, status)
		}
		if want == managed.ReasonRuntimeFailed && status.Error == "" {
			t.Errorf("%s: no reason given", stop)
		}
	}
}

func TestAnEndedSessionFails(t *testing.T) {
	f := newFakePlatform(t)
	f.hold = true
	r := f.runtime(t)
	ref, err := r.Start(context.Background(), gradedSpec())
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.sessions[ref.ID].session.Status = "ended"
	f.mu.Unlock()
	status, err := r.Poll(context.Background(), ref)
	if err != nil || status.State != managed.StateFailed || status.Error == "" {
		t.Fatalf("status = %+v, %v", status, err)
	}
}

func TestPlatformFailuresAreClassified(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/busy":
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
		case "/down":
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("not json"))
		case "/gone":
			writeFake(w, http.StatusNotFound, map[string]any{"error": map[string]string{"code": "not_found", "message": "gone"}})
		case "/refused":
			writeFake(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "forbidden", "message": "no"}})
		case "/big":
			_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBytes+1)))
		case "/garbled":
			_, _ = w.Write([]byte("{"))
		}
	}))
	defer server.Close()
	c := &client{base: server.URL, http: server.Client()}
	ctx := context.Background()
	err := c.call(ctx, "secret-bearer", http.MethodGet, "/busy", nil, "", nil)
	var temporary *managed.TemporaryError
	if !errors.As(err, &temporary) || temporary.After != 7*time.Second {
		t.Fatalf("busy = %v", err)
	}
	if err := c.call(ctx, "b", http.MethodGet, "/down", nil, "", nil); !managed.IsTemporary(err) || !strings.Contains(err.Error(), "502") {
		t.Fatalf("down = %v", err)
	}
	if err := c.call(ctx, "b", http.MethodGet, "/gone?path=private", nil, "", nil); !errors.Is(err, managed.ErrNotFound) || strings.Contains(err.Error(), "private") {
		t.Fatalf("gone = %v", err)
	}
	if err := c.call(ctx, "b", http.MethodGet, "/refused", nil, "", nil); code(err) != "forbidden" || managed.IsTemporary(err) {
		t.Fatalf("refused = %v", err)
	}
	if err := c.call(ctx, "b", http.MethodGet, "/big", nil, "", nil); err == nil {
		t.Fatal("an oversized answer was accepted")
	}
	var out map[string]any
	if err := c.call(ctx, "b", http.MethodGet, "/garbled", nil, "", &out); err == nil {
		t.Fatal("a garbled answer decoded")
	}
	if err := c.call(ctx, "b", http.MethodPost, "/x", func() {}, "", nil); err == nil {
		t.Fatal("an unencodable body was sent")
	}
	if err == nil || strings.Contains(err.Error(), "secret-bearer") {
		t.Fatalf("the bearer reached an error: %v", err)
	}
	closed := &client{base: "http://127.0.0.1:1", http: server.Client()}
	if err := closed.call(ctx, "b", http.MethodGet, "/", nil, "", nil); !managed.IsTemporary(err) {
		t.Fatalf("unreachable = %v", err)
	}
	if retryAfter("soon") != 0 || (&apiError{Status: 500}).Error() != "platform answered 500" || code(errors.New("x")) != "" {
		t.Fatal("helpers")
	}
	if defaultHTTPClient().Timeout == 0 {
		t.Fatal("the default client has no timeout")
	}
}

func TestAPersonalAccessTokenIsExchangedAndKept(t *testing.T) {
	var exchanges atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, key, _ := r.BasicAuth()
		if r.URL.Path != "/token" || user != "pat" || r.FormValue("grant_type") != "client_credentials" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch key {
		case "pat_good":
			exchanges.Add(1)
			writeFake(w, http.StatusOK, map[string]any{"access_token": "minted", "expires_in": 900})
		case "pat_empty":
			writeFake(w, http.StatusOK, map[string]any{"expires_in": 900})
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()
	source := PersonalAccessToken(server.URL+"/", "pat_good", server.Client())
	for range 3 {
		bearer, err := source.Token(context.Background())
		if err != nil || bearer != "minted" {
			t.Fatalf("Token = %q, %v", bearer, err)
		}
	}
	if exchanges.Load() != 1 {
		t.Fatalf("exchanged %d times, want once while the bearer is fresh", exchanges.Load())
	}
	expiring := source.(*cache)
	expiring.now = func() time.Time { return time.Now().Add(14*time.Minute + 30*time.Second) }
	if _, err := source.Token(context.Background()); err != nil || exchanges.Load() != 2 {
		t.Fatalf("a bearer due to expire was not replaced: %v, %d", err, exchanges.Load())
	}
	for _, key := range []string{"pat_bad", "pat_empty"} {
		if _, err := PersonalAccessToken(server.URL, key, server.Client()).Token(context.Background()); err == nil || strings.Contains(err.Error(), key) {
			t.Errorf("%s: %v", key, err)
		}
	}
	if _, err := PersonalAccessToken("http://127.0.0.1:1", "pat_x", server.Client()).Token(context.Background()); err == nil {
		t.Error("an unreachable Auth gave a bearer")
	}
	if bearer, err := PersonalAccessToken(server.URL, "pat_good", nil).Token(context.Background()); err != nil || bearer != "minted" {
		t.Errorf("with no client given: %q, %v", bearer, err)
	}
}

func TestACommandLendsABearer(t *testing.T) {
	bearer, err := Command("printf 'from-command\\n'").Token(context.Background())
	if err != nil || bearer != "from-command" {
		t.Fatalf("Token = %q, %v", bearer, err)
	}
	for _, command := range []string{"exit 3", "printf ''", "printf 'two words'"} {
		if _, err := Command(command).Token(context.Background()); err == nil {
			t.Errorf("%q gave a bearer", command)
		}
	}
}

func TestAContextSourceWinsAndNoneIsAnError(t *testing.T) {
	f := newFakePlatform(t)
	r := f.runtime(t)
	ctx := WithTokenSource(context.Background(), TokenFunc(func(context.Context) (string, error) { return "someone-else", nil }))
	if _, err := r.Capabilities(ctx); err == nil {
		t.Fatal("the runtime's own source was used over the context's")
	}
	bare, err := New(Config{BaseURL: f.server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bare.Capabilities(context.Background()); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("Capabilities = %v", err)
	}
	if bare.Name() != Name || bare.image != DefaultImage {
		t.Fatal("defaults")
	}
	if _, err := New(Config{BaseURL: "ftp://example.org"}); err == nil {
		t.Fatal("a non-http address was accepted")
	}
}

func TestALocatorFromAnotherSchemaIsRefused(t *testing.T) {
	ref := managed.SessionRef{Runtime: Name, ID: "ses_1", Revision: 1, LocatorVersion: 2, Locator: json.RawMessage(`{}`)}
	if _, err := decode(ref); !errors.Is(err, managed.ErrContract) {
		t.Fatalf("decode = %v", err)
	}
	ref.LocatorVersion = LocatorVersion
	ref.Locator = json.RawMessage(`[]`)
	if _, err := decode(ref); !errors.Is(err, managed.ErrContract) {
		t.Fatalf("decode = %v", err)
	}
}

func TestSleepHonorsTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepContext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleep = %v", err)
	}
	if err := sleepContext(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
}
