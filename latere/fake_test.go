// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePlatform is a stand-in for the platform's API: Topos's agents and
// sessions, the account's preferences and Effort levels, the model catalog
// and Storage. A message runs its turn at once, so a test needs no waiting:
// a task writes the scripted files and goes idle, a request to grade starts
// the grader thread, which answers with the next scripted verdict.
type fakePlatform struct {
	t      *testing.T
	server *httptest.Server

	mu         sync.Mutex
	preference string
	agents     map[string][]json.RawMessage
	sessions   map[string]*fakeSession
	idem       map[string]json.RawMessage
	next       int
	// files is what the worker writes into its working directory on every
	// turn, by path.
	files map[string]string
	// verdicts are the grader's answers, one per grading, the last repeated.
	verdicts []string
	graded   int
	// hold leaves a turn running until it is interrupted.
	hold bool
	// stop is the stop reason a worker turn ends with.
	stop string
	// spawn edits the task the worker spawns the grader with.
	spawn func(task string) (agent, sent string, tools []string)
	// fail answers the next request to a path prefix with a status, once.
	fail map[string]int
	// requests counts requests by method and path prefix.
	requests map[string]int
}

type fakeSession struct {
	session wireSession
	agent   string
	events  []wireEvent
	deleted bool
	files   map[string]string
}

const fakeSubject = "https://auth.example|person-1"

func newFakePlatform(t *testing.T) *fakePlatform {
	t.Helper()
	f := &fakePlatform{
		t: t, preference: "none", agents: map[string][]json.RawMessage{}, sessions: map[string]*fakeSession{},
		idem: map[string]json.RawMessage{}, files: map[string]string{"outputs/result.txt": "5\n"},
		verdicts: []string{passed}, stop: "end_turn", fail: map[string]int{}, requests: map[string]int{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

const passed = `I read the files. {"grade": "passed", "explanation": "It reproduces.", "criteria": [{"id": 1, "name": "result", "passed": true, "evidence": "outputs/result.txt"}]}`

const failed = `{"grade": "failed", "explanation": "It does not.", "criteria": [{"id": 1, "name": "result", "passed": false, "evidence": "outputs/result.txt is wrong"}]}`

const needsRevision = "```json\n{\"grade\": \"needs_revision\", \"explanation\": \"Close.\", \"criteria\": [{\"id\": 1, \"name\": \"result\", \"passed\": false, \"evidence\": \"missing a unit\"}]}\n```"

func (f *fakePlatform) runtime(t *testing.T) *Runtime {
	t.Helper()
	r, err := New(Config{BaseURL: f.server.URL, HTTP: f.server.Client(), Tokens: TokenFunc(func(ctx context.Context) (string, error) { return "bearer-1", nil })})
	if err != nil {
		t.Fatal(err)
	}
	r.sleep = func(context.Context, time.Duration) error { return nil }
	return r
}

func (f *fakePlatform) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[key]
}

func (f *fakePlatform) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer bearer-1" {
		writeFake(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "unauthorized"}})
		return
	}
	path := r.URL.Path
	f.requests[r.Method+" "+route(path)]++
	for prefix, status := range f.fail {
		if strings.HasPrefix(r.Method+" "+path, prefix) {
			delete(f.fail, prefix)
			writeFake(w, status, map[string]any{"error": map[string]string{"code": http.StatusText(status), "message": "injected"}})
			return
		}
	}
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		if answer, ok := f.idem[key]; ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(answer)
			return
		}
		recorder := httptest.NewRecorder()
		f.route(recorder, r)
		if recorder.Code/100 == 2 {
			f.idem[key] = recorder.Body.Bytes()
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
		return
	}
	f.route(w, r)
}

// route reduces a path to its shape, for counting.
func route(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, "ses_") || strings.HasPrefix(part, DefaultProduct.Key+"-") {
			parts[i] = "{id}"
		}
	}
	if len(parts) > 6 && parts[2] == "storage" {
		parts = parts[:5]
	}
	return strings.Join(parts, "/")
}

func (f *fakePlatform) route(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/v1/account/preferences":
		writeFake(w, http.StatusOK, map[string]any{"network": map[string]string{"mode": f.preference}})
	case r.Method == http.MethodGet && path == "/v1/account/effort":
		writeFake(w, http.StatusOK, map[string]any{"levels": []map[string]any{
			{"id": "quick", "name": "tier/quick", "available": true, "models": []map[string]any{
				{"name": "free/google/gemma", "charge": "none", "available": true},
				{"name": "google/gemma", "charge": "credit", "available": true}}},
			{"id": "thorough", "name": "tier/thorough", "available": true, "next": map[string]any{"price": map[string]any{"per": 1000000, "input": "2", "output": "10"}}, "models": []map[string]any{
				{"name": "anthropic/claude-sonnet-5.5", "charge": "credit", "available": true}}},
			{"id": "sustained", "name": "tier/sustained", "available": true, "models": []map[string]any{
				{"name": "anthropic/claude-opus-5.5", "charge": "credit", "available": false},
				{"name": "moonshotai/kimi-k3", "charge": "credit", "available": true}}},
			{"id": "free-only", "name": "tier/free-only", "available": true, "models": []map[string]any{
				{"name": "free/google/gemma", "charge": "none", "available": true}}},
		}})
	case r.Method == http.MethodGet && path == "/v1/models/models":
		f.catalog(w, r)
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/v1/agents/agents/"):
		name := strings.TrimPrefix(path, "/v1/agents/agents/")
		var manifest json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&manifest); err != nil {
			writeFake(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_manifest"}})
			return
		}
		versions := f.agents[name]
		if len(versions) == 0 || string(versions[len(versions)-1]) != string(manifest) {
			versions = append(versions, manifest)
			f.agents[name] = versions
		}
		writeFake(w, http.StatusOK, map[string]any{"status": map[string]any{"version": len(versions)}})
	case r.Method == http.MethodPost && path == "/v1/agents/sessions":
		f.create(w, r)
	case strings.HasPrefix(path, "/v1/agents/sessions/"):
		f.session(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/storage/files/"):
		f.storage(w, r)
	default:
		writeFake(w, http.StatusNotFound, map[string]any{"error": map[string]string{"code": "not_found"}})
	}
}

func (f *fakePlatform) catalog(w http.ResponseWriter, r *http.Request) {
	all := []map[string]any{
		{"metadata": map[string]string{"name": "anthropic/claude-sonnet-4.6"}, "status": map[string]bool{"available": true}},
		{"metadata": map[string]string{"name": "openai/gpt-6.1-sol"}, "status": map[string]bool{"available": true}},
		{"metadata": map[string]string{"name": "mistralai/retired"}, "spec": map[string]bool{"disabled": true}, "status": map[string]bool{"available": true}},
	}
	if r.URL.Query().Get("cursor") == "" {
		writeFake(w, http.StatusOK, map[string]any{"items": all[:1], "next_cursor": "page-2"})
		return
	}
	writeFake(w, http.StatusOK, map[string]any{"items": all[1:]})
}

func (f *fakePlatform) create(w http.ResponseWriter, r *http.Request) {
	var body createSession
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeFake(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_request"}})
		return
	}
	name, versionText, _ := strings.Cut(body.Agent, "@")
	version, _ := strconv.Atoi(versionText)
	if version < 1 || version > len(f.agents[name]) {
		writeFake(w, http.StatusNotFound, map[string]any{"error": map[string]string{"code": "agent_not_found"}})
		return
	}
	var manifest agentManifest
	_ = json.Unmarshal(f.agents[name][version-1], &manifest)
	f.next++
	id := fmt.Sprintf("ses_%04d", f.next)
	session := wireSession{
		ID: id, Status: "idle", Network: wireNetwork{Mode: f.preference, Source: "authorizer"},
		Metadata: body.Metadata, CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
	session.Initiator.Subject = fakeSubject
	if level, ok := strings.CutPrefix(manifest.Spec.Model.Name, "tier/"); ok {
		session.Model = &wireModelRef{Name: "vendor/" + level + "-model", Via: manifest.Spec.Model.Name}
	}
	f.sessions[id] = &fakeSession{session: session, agent: body.Agent, files: map[string]string{}}
	writeFake(w, http.StatusCreated, session)
}

func (f *fakePlatform) session(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/agents/sessions/")
	id, tail, _ := strings.Cut(rest, "/")
	s, ok := f.sessions[id]
	if !ok || s.deleted {
		writeFake(w, http.StatusNotFound, map[string]any{"error": map[string]string{"code": "not_found"}})
		return
	}
	switch {
	case r.Method == http.MethodGet && tail == "":
		writeFake(w, http.StatusOK, s.session)
	case r.Method == http.MethodPatch && tail == "":
		var body struct {
			Metadata map[string]string `json:"metadata"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Metadata != nil {
			s.session.Metadata = body.Metadata
		}
		writeFake(w, http.StatusOK, s.session)
	case r.Method == http.MethodDelete && tail == "":
		if s.session.Status == "running" {
			writeFake(w, http.StatusConflict, map[string]any{"error": map[string]string{"code": "conflict"}})
			return
		}
		s.deleted = true
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && tail == "events":
		f.events(w, r, s)
	case r.Method == http.MethodPost && tail == "events":
		f.send(w, r, s)
	case r.Method == http.MethodGet && tail == "files":
		body, ok := s.files[r.URL.Query().Get("path")]
		if !ok {
			writeFake(w, http.StatusNotFound, map[string]any{"error": map[string]string{"code": "not_found"}})
			return
		}
		_, _ = w.Write([]byte(body))
	default:
		writeFake(w, http.StatusNotFound, map[string]any{"error": map[string]string{"code": "not_found"}})
	}
}

func (f *fakePlatform) events(w http.ResponseWriter, r *http.Request, s *fakeSession) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 2 {
		// A page of two makes every test follow the cursor.
		limit = 2
	}
	from, _ := strconv.ParseUint(r.URL.Query().Get("from_seq"), 10, 64)
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		from, _ = strconv.ParseUint(cursor, 10, 64)
	}
	var items []wireEvent
	for _, event := range s.events {
		if event.Seq >= from && len(items) < limit {
			items = append(items, event)
		}
	}
	answer := map[string]any{"items": items}
	if len(items) == limit && items[len(items)-1].Seq < uint64(len(s.events)) {
		answer["next_cursor"] = strconv.FormatUint(items[len(items)-1].Seq+1, 10)
	}
	writeFake(w, http.StatusOK, answer)
}

func (f *fakePlatform) append(s *fakeSession, kind, thread string, payload any) wireEvent {
	raw, err := json.Marshal(payload)
	if err != nil {
		f.t.Fatal(err)
	}
	event := wireEvent{
		ID: fmt.Sprintf("evt_%s_%d", s.session.ID, len(s.events)+1), Seq: uint64(len(s.events) + 1),
		Type: kind, Time: time.Unix(1_700_000_000+int64(len(s.events)), 0).UTC(), Thread: thread, Payload: raw,
	}
	s.events = append(s.events, event)
	s.session.LastSeq = event.Seq
	return event
}

func (f *fakePlatform) send(w http.ResponseWriter, r *http.Request, s *fakeSession) {
	var body struct {
		Type    string          `json:"type"`
		Payload wireUserMessage `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeFake(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_request"}})
		return
	}
	if body.Type == "user.interrupt" {
		event := f.append(s, "user.interrupt", "", map[string]any{})
		if s.session.Status == "running" {
			f.append(s, "session.status", "", wireStatus{Status: "idle", StopReason: "interrupted"})
			s.session.Status = "idle"
		}
		writeFake(w, http.StatusOK, event)
		return
	}
	event := f.append(s, "user.message", "", body.Payload)
	writeFake(w, http.StatusOK, event)
	f.append(s, "session.status", "", wireStatus{Status: "running"})
	s.session.Status = "running"
	if f.hold {
		return
	}
	message := text(body.Payload.Content)
	if strings.Contains(message, "Call the spawn tool") {
		f.grade(s, message)
	} else {
		f.work(s)
	}
	if s.session.Status == "running" {
		f.append(s, "session.status", "", wireStatus{Status: "idle", StopReason: f.stop})
		s.session.Status = "idle"
	}
}

func (f *fakePlatform) work(s *fakeSession) {
	cached, written := int64(10), int64(5)
	cost := int64(1200)
	f.append(s, "model.request", "", wireModelRequest{Model: "vendor/thorough-model", Outcome: "ok", Cost: &cost,
		Usage: &wireUsage{InputTokens: 100, OutputTokens: 20, CacheRead: &cached, CacheWrite: &written, ReasoningTokens: 7}})
	f.append(s, "agent.message", "", wireAgentMessage{Message: struct {
		Blocks []wireBlock `json:"blocks"`
	}{Blocks: []wireBlock{{Type: "thinking", Thinking: "plan"}}}})
	f.append(s, "agent.tool_use", "", wireToolUse{ToolUseID: "t1", Name: "write", Input: json.RawMessage(`{"path":"outputs/result.txt"}`)})
	f.append(s, "tool.result", "", wireToolResult{ToolUseID: "t1", Content: []wireBlock{{Type: "text", Text: "Created."}}})
	maps.Copy(s.files, f.files)
	s.files["attachments/evt_1/paper.pdf"] = "%PDF"
	f.append(s, "work.saved", "", map[string]any{"covers": len(s.events)})
	f.append(s, "agent.message", "", wireAgentMessage{Message: struct {
		Blocks []wireBlock `json:"blocks"`
	}{Blocks: []wireBlock{{Type: "text", Text: "Done."}}}})
}

func (f *fakePlatform) grade(s *fakeSession, message string) {
	_, after, _ := strings.Cut(message, "-----\n")
	task, _, _ := strings.Cut(after, "\n-----")
	agent, tools := graderName, append([]string(nil), graderTools...)
	if f.spawn != nil {
		agent, task, tools = f.spawn(task)
	}
	if agent == "" {
		f.append(s, "agent.message", "", wireAgentMessage{Message: struct {
			Blocks []wireBlock `json:"blocks"`
		}{Blocks: []wireBlock{{Type: "text", Text: `I graded it myself: {"grade": "passed", "explanation": "x", "criteria": [{"id": 1, "name": "x", "passed": true, "evidence": "x"}]}`}}}})
		return
	}
	f.append(s, "agent.tool_use", "", wireToolUse{ToolUseID: "spawn-1", Name: "spawn"})
	started := f.append(s, "thread.started", "", wireThreadStarted{Task: task, Tools: tools, Model: "anthropic/claude-sonnet-5.5"})
	thread := started.ID
	s.events[len(s.events)-1].Thread = thread
	var startedPayload wireThreadStarted
	_ = json.Unmarshal(started.Payload, &startedPayload)
	startedPayload.Agent.Name = agent
	raw, _ := json.Marshal(startedPayload)
	s.events[len(s.events)-1].Payload = raw
	verdict := f.verdicts[min(f.graded, len(f.verdicts)-1)]
	f.graded++
	cost := int64(300)
	f.append(s, "model.request", thread, wireModelRequest{Model: "anthropic/claude-sonnet-5.5", Outcome: "ok", Cost: &cost, Usage: &wireUsage{InputTokens: 50, OutputTokens: 10}})
	f.append(s, "agent.message", thread, wireAgentMessage{Message: struct {
		Blocks []wireBlock `json:"blocks"`
	}{Blocks: []wireBlock{{Type: "text", Text: verdict}}}})
	f.append(s, "tool.result", "", wireToolResult{ToolUseID: "spawn-1", Content: []wireBlock{{Type: "text", Text: verdict}}})
}

func (f *fakePlatform) storage(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.EscapedPath(), "/v1/storage/files/")
	escaped, path, _ := strings.Cut(rest, "/")
	if owner, err := url.PathUnescape(escaped); err != nil || owner != fakeSubject {
		writeFake(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "forbidden"}})
		return
	}
	id := strings.TrimPrefix(path, "files/conversations/")
	id, file, _ := strings.Cut(id, "/work")
	s, ok := f.sessions[id]
	if !ok || s.deleted || len(s.files) == 0 {
		writeFake(w, http.StatusNotFound, map[string]any{"error": map[string]string{"code": "not_found"}})
		return
	}
	if r.URL.Query().Get("list") == "1" {
		var entries []wireStorageEntry
		for name, body := range s.files {
			entries = append(entries, wireStorageEntry{Path: "files/conversations/" + id + "/work/" + name, Size: int64(len(body))})
		}
		entries = append(entries, wireStorageEntry{Path: "files/conversations/" + id + "/work/__pycache__/x.pyc"})
		slices.SortFunc(entries, func(a, b wireStorageEntry) int { return strings.Compare(a.Path, b.Path) })
		// Storage answers a page at a time. A page of two makes every test
		// follow the cursor, as a real working directory does.
		from, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
		size := 2
		if limit, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && limit < size {
			size = limit
		}
		end := min(from+size, len(entries))
		answer := map[string]any{"entries": entries[from:end]}
		if end < len(entries) {
			answer["next_cursor"] = strconv.Itoa(end)
		}
		writeFake(w, http.StatusOK, answer)
		return
	}
	body, ok := s.files[strings.TrimPrefix(file, "/")]
	if !ok {
		writeFake(w, http.StatusNotFound, map[string]any{"error": map[string]string{"code": "not_found"}})
		return
	}
	_, _ = w.Write([]byte(body))
}

func writeFake(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
