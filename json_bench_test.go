// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func benchGolden(b *testing.B, name string) []byte {
	b.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		b.Fatal(err)
	}
	return body
}

func benchRoundTrip[T any](b *testing.B, body []byte) {
	b.Helper()
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	for b.Loop() {
		var v T
		if err := json.Unmarshal(body, &v); err != nil {
			b.Fatal(err)
		}
		if _, err := json.Marshal(v); err != nil {
			b.Fatal(err)
		}
	}
}

// A session's status as a runtime reports it on every poll: the state and
// grade enums use the contract's own codecs.
func BenchmarkJSONStatusRoundTrip(b *testing.B) {
	benchRoundTrip[Status](b, benchGolden(b, "Status"))
}

// The spec a caller starts a session with.
func BenchmarkJSONSessionSpecRoundTrip(b *testing.B) {
	benchRoundTrip[SessionSpec](b, benchGolden(b, "SessionSpec"))
}

// One transcript entry, from the golden value up to the large tool result a
// long agent run appends.
func BenchmarkJSONEventRoundTrip(b *testing.B) {
	var golden Event
	if err := json.Unmarshal(benchGolden(b, "Event"), &golden); err != nil {
		b.Fatal(err)
	}
	line := "ok  \tlatere.ai/x/example/pkg\t0.412s\tcoverage: 81.3% of statements\n"
	for _, c := range []struct {
		name  string
		bytes int
	}{{"small", 0}, {"medium", 10 << 10}, {"large", 200 << 10}} {
		e := golden
		e.Time = time.Date(2026, 8, 24, 9, 4, 11, 0, time.UTC)
		if c.bytes > 0 {
			e.Kind, e.Tool = KindToolResult, "bash"
			e.Content = strings.Repeat(line, c.bytes/len(line))
		}
		body, err := json.Marshal(e)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(c.name, func(b *testing.B) { benchRoundTrip[Event](b, body) })
	}
}

// The check every ref goes through on its way in: the locator is parsed and
// walked for secret-bearing keys.
func BenchmarkJSONSessionRefValidate(b *testing.B) {
	var ref SessionRef
	body := benchGolden(b, "SessionRef")
	if err := json.Unmarshal(body, &ref); err != nil {
		b.Fatal(err)
	}
	ref.Locator = json.RawMessage(`{"agent":"review-3f9a1c7e5b2d4a60","model":"claude-sonnet-5-5","graded":true,"max_revisions":2,"task_seq":41,"started":"2026-08-24T09:00:00Z"}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(ref.Locator)))
	for b.Loop() {
		if err := ref.Validate(DefaultMaxLocatorBytes); err != nil {
			b.Fatal(err)
		}
	}
}
