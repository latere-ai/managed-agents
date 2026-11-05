// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// cannedTransport answers every request with one fixed body, so the
// benchmarks run the client's own encode, send and decode without a socket.
type cannedTransport struct{ body []byte }

func (t cannedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return nil, err
		}
		_ = r.Body.Close()
	}
	return &http.Response{
		StatusCode: http.StatusOK, Status: "200 OK", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:  http.Header{"Content-Type": {"application/json"}},
		Body:    io.NopCloser(bytes.NewReader(t.body)),
		Request: r,
	}, nil
}

func benchPlatform(body []byte) platform {
	return platform{client: &client{base: "https://platform.invalid", http: &http.Client{Transport: cannedTransport{body: body}}}}
}

func benchRaw(b *testing.B, v any) json.RawMessage {
	b.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		b.Fatal(err)
	}
	return raw
}

// benchEvents is a session log of count events: a user message, then turns
// of model request, tool use, tool result with resultBytes of output, and an
// agent message, closed by an idle status.
func benchEvents(b *testing.B, count, resultBytes int) []wireEvent {
	b.Helper()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	output := strings.Repeat("--- PASS: TestSomething (0.01s)\n", resultBytes/16+2)[:resultBytes]
	cost, cached := int64(18400), int64(61200)
	var events []wireEvent
	add := func(kind string, payload any) {
		n := len(events) + 1
		events = append(events, wireEvent{
			ID: fmt.Sprintf("evt_01JA7Z%020d", n), Seq: uint64(n), Type: kind,
			Time: base.Add(time.Duration(n) * 1500 * time.Millisecond), Turn: n/5 + 1, Payload: benchRaw(b, payload),
		})
	}
	add("user.message", wireUserMessage{Content: []wireBlock{{Type: "text", Text: "Run the package tests, then fix whatever fails and summarize what you changed."}}})
	for len(events) < count-1 {
		add("model.request", wireModelRequest{Model: "claude-sonnet-5-5", Outcome: "ok", Cost: &cost,
			Usage: &wireUsage{InputTokens: 1840, OutputTokens: 412, CacheRead: &cached}})
		add("agent.tool_use", wireToolUse{ToolUseID: fmt.Sprintf("toolu_%016d", len(events)), Name: "bash",
			Input: json.RawMessage(`{"command":"go test ./internal/... 2>&1 | tail -n 80","timeout":120}`)})
		add("tool.result", wireToolResult{ToolUseID: fmt.Sprintf("toolu_%016d", len(events)-1), Content: []wireBlock{{Type: "text", Text: output}}})
		add("agent.message", wireAgentMessage{StopReason: "end_turn", Message: struct {
			Blocks []wireBlock `json:"blocks"`
		}{Blocks: []wireBlock{{Type: "text", Text: "All packages pass after the fix. The failing case was a stale fixture in the store tests; I regenerated it and the run is green."}}}})
	}
	add("session.status", wireStatus{Status: "idle", StopReason: "end_turn"})
	return events[:min(len(events), count)]
}

var benchSizes = []struct {
	name         string
	count, bytes int
}{{"small", 5, 120}, {"medium", 20, 1200}, {"large", 400, 480}}

// The page of session events the adapter reads on every poll.
func BenchmarkJSONSessionEventsDecode(b *testing.B) {
	for _, c := range benchSizes {
		body := benchRaw(b, map[string]any{"items": benchEvents(b, c.count, c.bytes)})
		p := benchPlatform(body)
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				events, err := p.events(context.Background(), "token", "ses_01JA7Z4K", 0)
				if err != nil {
					b.Fatal(err)
				}
				if len(events) != c.count {
					b.Fatalf("%d events, want %d", len(events), c.count)
				}
			}
		})
	}
}

// The log turned into the contract's transcript: each payload is decoded by
// the type of its event.
func BenchmarkJSONTranscriptNormalizeUnmarshal(b *testing.B) {
	for _, c := range benchSizes {
		events := benchEvents(b, c.count, c.bytes)
		size := 0
		for _, e := range events {
			size += len(e.Payload)
		}
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				if got := normalize(events); len(got) != len(events) {
					b.Fatalf("%d entries, want %d", len(got), len(events))
				}
			}
		})
	}
}

// The grading loop replays the log to find where it is, decoding every user
// message.
func BenchmarkJSONReplayUnmarshal(b *testing.B) {
	events := benchEvents(b, 400, 480)
	for i := 0; i < len(events); i += 20 {
		events[i].Type = "user.message"
		events[i].Payload = benchRaw(b, wireUserMessage{Content: []wireBlock{{Type: "text", Text: "Please also check the race detector."}}})
	}
	size := 0
	for _, e := range events {
		if e.Type == "user.message" {
			size += len(e.Payload)
		}
	}
	b.ReportAllocs()
	b.SetBytes(int64(size))
	for b.Loop() {
		replay(events, locator{TaskSeq: 0})
	}
}

// A message sent to a session: the request is encoded and the appended event
// decoded from the answer.
func BenchmarkJSONSessionMessageRoundTrip(b *testing.B) {
	answer := benchRaw(b, benchEvents(b, 1, 0)[0])
	for _, c := range []struct {
		name string
		text int
	}{{"small", 300}, {"medium", 10 << 10}, {"large", 200 << 10}} {
		text := strings.Repeat("Review the diff and tell me what could break. ", c.text/46+1)[:c.text]
		p := benchPlatform(answer)
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(text)))
			for b.Loop() {
				if _, err := p.message(context.Background(), "token", "ses_01JA7Z4K", text, nil, "idem-0001"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// The session ref's locator, written when a session starts and read back on
// every poll.
func BenchmarkJSONLocatorRoundTrip(b *testing.B) {
	loc := locator{Agent: "review-3f9a1c7e5b2d4a60", Model: "claude-sonnet-5-5", Graded: true, MaxRevisions: 2, TaskSeq: 41,
		Started: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	ref, err := reference("ses_01JA7Z4K", 3, loc)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(ref.Locator)))
	for b.Loop() {
		ref, err := reference("ses_01JA7Z4K", 3, loc)
		if err != nil {
			b.Fatal(err)
		}
		got, err := decode(ref)
		if err != nil {
			b.Fatal(err)
		}
		if got.TaskSeq != loc.TaskSeq {
			b.Fatal("locator changed")
		}
	}
}
